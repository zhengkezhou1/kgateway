package extauth

import (
	"path/filepath"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1"
	"github.com/kgateway-dev/kgateway/v2/pkg/utils/fsutils"
)

var (
	// common resources
	simpleSvc = &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple-svc",
			Namespace: "default",
		},
	}
	simpleDeployment = &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend-0",
			Namespace: "default",
		},
	}

	proxyObjMeta = metav1.ObjectMeta{
		Name:      "super-gateway",
		Namespace: "default",
	}
	proxyDeployment = &appsv1.Deployment{ObjectMeta: proxyObjMeta}
	proxyService    = &corev1.Service{ObjectMeta: proxyObjMeta}

	// ExtAuth service and extension
	extAuthSvc = &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ext-authz",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: 8000,
				},
			},
			Selector: map[string]string{
				"app.kubernetes.io/name": "extauth",
			},
		},
	}

	extAuthExtension = &v1alpha1.GatewayExtension{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "basic-extauth",
			Namespace: "default",
		},
		Spec: v1alpha1.GatewayExtensionSpec{
			Type: v1alpha1.GatewayExtensionTypeExtAuth,
			ExtAuth: &v1alpha1.ExtAuthProvider{
				GrpcService: &v1alpha1.ExtGrpcService{
					BackendRef: &gwv1.BackendRef{
						BackendObjectReference: gwv1.BackendObjectReference{
							Name: "ext-authz",
						},
					},
				},
			},
		},
	}

	// MARK per test data
	basicSecureRoute = &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hey-its-a-route",
			Namespace: "default",
		},
	}
	gatewayAttachedTrafficPolicy = &v1alpha1.TrafficPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gw-policy",
			Namespace: "default",
		},
	}
	insecureRoute = &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "route-example-insecure",
			Namespace: "default",
		},
	}
	insecureTrafficPolicy = &v1alpha1.TrafficPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "insecure-route-policy",
			Namespace: "default",
		},
	}
	secureRoute = &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "route-example-secure",
			Namespace: "default",
		},
	}
	secureTrafficPolicy = &v1alpha1.TrafficPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secure-route-policy",
			Namespace: "default",
		},
	}

	// Manifest files
	gatewayWithRouteManifest     = getTestFile("common.yaml")
	simpleServiceManifest        = getTestFile("service.yaml")
	extAuthManifest              = getTestFile("ext-authz-server.yaml")
	securedGatewayPolicyManifest = getTestFile("secured-gateway-policy.yaml")
	securedRouteManifest         = getTestFile("secured-route.yaml")
	insecureRouteManifest        = getTestFile("insecure-route.yaml")
)

func getTestFile(filename string) string {
	return filepath.Join(fsutils.MustGetThisDir(), "testdata", filename)
}
