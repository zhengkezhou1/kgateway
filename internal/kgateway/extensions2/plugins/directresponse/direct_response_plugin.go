package directresponse

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	envoycorev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoyroutev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	skubeclient "istio.io/istio/pkg/config/schema/kubeclient"
	"istio.io/istio/pkg/kube/kclient"
	"istio.io/istio/pkg/kube/krt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/extensions2/common"
	extensionplug "github.com/kgateway-dev/kgateway/v2/internal/kgateway/extensions2/plugin"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/ir"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/wellknown"
	"github.com/kgateway-dev/kgateway/v2/pkg/client/clientset/versioned"
	"github.com/kgateway-dev/kgateway/v2/pkg/reports"
)

type directResponse struct {
	ct   time.Time
	spec v1alpha1.DirectResponseSpec
}

// in case multiple policies attached to the same resource, we sort by policy creation time.
func (d *directResponse) CreationTime() time.Time {
	return d.ct
}

func (d *directResponse) Equals(in any) bool {
	d2, ok := in.(*directResponse)
	if !ok {
		return false
	}
	return d.spec == d2.spec
}

type directResponsePluginGwPass struct {
	ir.UnimplementedProxyTranslationPass
	reporter reports.Reporter
}

var _ ir.ProxyTranslationPass = &directResponsePluginGwPass{}

func registerTypes(ourCli versioned.Interface) {
	skubeclient.Register[*v1alpha1.DirectResponse](
		wellknown.DirectResponseGVR,
		wellknown.DirectResponseGVK,
		func(c skubeclient.ClientGetter, namespace string, o metav1.ListOptions) (runtime.Object, error) {
			return ourCli.GatewayV1alpha1().DirectResponses(namespace).List(context.Background(), o)
		},
		func(c skubeclient.ClientGetter, namespace string, o metav1.ListOptions) (watch.Interface, error) {
			return ourCli.GatewayV1alpha1().DirectResponses(namespace).Watch(context.Background(), o)
		},
	)
}

func NewPlugin(ctx context.Context, commoncol *common.CommonCollections) extensionplug.Plugin {
	registerTypes(commoncol.OurClient)

	col := krt.WrapClient(kclient.NewFiltered[*v1alpha1.DirectResponse](
		commoncol.Client,
		kclient.Filter{ObjectFilter: commoncol.Client.ObjectFilter()},
	), commoncol.KrtOpts.ToOptions("DirectResponse")...)

	gk := wellknown.DirectResponseGVK.GroupKind()
	policyCol := krt.NewCollection(col, func(krtctx krt.HandlerContext, i *v1alpha1.DirectResponse) *ir.PolicyWrapper {
		pol := &ir.PolicyWrapper{
			ObjectSource: ir.ObjectSource{
				Group:     gk.Group,
				Kind:      gk.Kind,
				Namespace: i.Namespace,
				Name:      i.Name,
			},
			Policy:   i,
			PolicyIR: &directResponse{ct: i.CreationTimestamp.Time, spec: i.Spec},
			// no target refs for direct response
		}
		return pol
	})

	return extensionplug.Plugin{
		ContributesPolicies: map[schema.GroupKind]extensionplug.PolicyPlugin{
			wellknown.DirectResponseGVK.GroupKind(): {
				Name:                      "directresponse",
				Policies:                  policyCol,
				NewGatewayTranslationPass: NewGatewayTranslationPass,
			},
		},
	}
}

func NewGatewayTranslationPass(ctx context.Context, tctx ir.GwTranslationCtx, reporter reports.Reporter) ir.ProxyTranslationPass {
	return &directResponsePluginGwPass{
		reporter: reporter,
	}
}

// called one or more times per route rule
func (p *directResponsePluginGwPass) ApplyForRoute(ctx context.Context, pCtx *ir.RouteContext, outputRoute *envoyroutev3.Route) error {
	dr, ok := pCtx.Policy.(*directResponse)
	if !ok {
		return fmt.Errorf("internal error: expected *directResponse, got %T", pCtx.Policy)
	}
	// at this point, we have a valid DR reference that we should apply to the route.
	if outputRoute.GetAction() != nil {
		// the output route already has an action, which is incompatible with the DirectResponse,
		// so we'll return an error. note: the direct response plugin runs after other route plugins
		// that modify the output route (e.g. the redirect plugin), so this should be a rare case.
		outputRoute.Action = &envoyroutev3.Route_DirectResponse{
			DirectResponse: &envoyroutev3.DirectResponseAction{
				Status: http.StatusInternalServerError,
			},
		}
		return fmt.Errorf("DirectResponse cannot be applied to route with existing action: %T", outputRoute.GetAction())
	}

	drAction := &envoyroutev3.DirectResponseAction{
		Status: dr.spec.StatusCode,
	}
	if dr.spec.Body != nil {
		drAction.Body = &envoycorev3.DataSource{
			Specifier: &envoycorev3.DataSource_InlineString{
				InlineString: *dr.spec.Body,
			},
		}
	}
	outputRoute.Action = &envoyroutev3.Route_DirectResponse{
		DirectResponse: drAction,
	}

	return nil
}

func (p *directResponsePluginGwPass) ApplyForRouteBackend(
	ctx context.Context,
	policy ir.PolicyIR,
	pCtx *ir.RouteBackendContext,
) error {
	return ir.ErrNotAttachable
}
