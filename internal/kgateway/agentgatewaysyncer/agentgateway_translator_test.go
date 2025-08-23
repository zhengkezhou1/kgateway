package agentgatewaysyncer

import (
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/wellknown"
	"github.com/kgateway-dev/kgateway/v2/pkg/reports"
	"github.com/kgateway-dev/kgateway/v2/pkg/utils/fsutils"
)

type translatorTestCase struct {
	inputFile     string
	outputFile    string
	assertReports AssertReports
	gwNN          types.NamespacedName
}

var _ = DescribeTable("Basic agentgateway Tests",
	func(in translatorTestCase, settingOpts ...SettingsOpts) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		dir := fsutils.MustGetThisDir()

		inputFiles := []string{filepath.Join(dir, "testdata/inputs/", in.inputFile)}
		expectedProxyFile := filepath.Join(dir, "testdata/outputs/", in.outputFile)
		TestTranslation(GinkgoT(), ctx, inputFiles, expectedProxyFile, in.gwNN, in.assertReports, settingOpts...)
	},
	Entry(
		"http gateway with basic http routing",
		translatorTestCase{
			inputFile:  "http-routing",
			outputFile: "http-routing-proxy.yaml",
			gwNN: types.NamespacedName{
				Namespace: "default",
				Name:      "example-gateway",
			},
		}),
	Entry(
		"grpc gateway with basic routing",
		translatorTestCase{
			inputFile:  "grpc-routing/basic.yaml",
			outputFile: "grpc-routing/basic-proxy.yaml",
			gwNN: types.NamespacedName{
				Namespace: "default",
				Name:      "example-gateway",
			},
			assertReports: func(gwNN types.NamespacedName, reportsMap reports.ReportMap) {
				route := &gwv1.GRPCRoute{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "example-grpc-route",
						Namespace: "default",
					},
				}
				routeStatus := reportsMap.BuildRouteStatus(context.Background(), route, wellknown.DefaultGatewayClassName)
				Expect(routeStatus).NotTo(BeNil())
				Expect(routeStatus.Parents).To(HaveLen(1))
				resolvedRefs := meta.FindStatusCondition(routeStatus.Parents[0].Conditions, string(gwv1.RouteConditionResolvedRefs))
				Expect(resolvedRefs).NotTo(BeNil())
				Expect(resolvedRefs.Status).To(Equal(metav1.ConditionTrue))
				Expect(resolvedRefs.Reason).To(Equal(string(gwv1.RouteReasonResolvedRefs)))
			},
		}),
	Entry(
		"grpcroute with missing backend reports correctly",
		translatorTestCase{
			inputFile:  "grpc-routing/missing-backend.yaml",
			outputFile: "grpc-routing/missing-backend.yaml",
			gwNN: types.NamespacedName{
				Namespace: "default",
				Name:      "example-gateway",
			},
			assertReports: func(gwNN types.NamespacedName, reportsMap reports.ReportMap) {
				route := &gwv1.GRPCRoute{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "example-grpc-route",
						Namespace: "default",
					},
				}
				routeStatus := reportsMap.BuildRouteStatus(context.Background(), route, wellknown.DefaultGatewayClassName)
				Expect(routeStatus).NotTo(BeNil())
				Expect(routeStatus.Parents).To(HaveLen(1))
				resolvedRefs := meta.FindStatusCondition(routeStatus.Parents[0].Conditions, string(gwv1.RouteConditionResolvedRefs))
				Expect(resolvedRefs).NotTo(BeNil())
				Expect(resolvedRefs.Status).To(Equal(metav1.ConditionFalse))
				Expect(resolvedRefs.Message).To(Equal(`backend(example-grpc-svc.default.svc.cluster.local) not found`))
			},
		}),
	Entry(
		"grpcroute with invalid backend reports correctly",
		translatorTestCase{
			inputFile:  "grpc-routing/invalid-backend.yaml",
			outputFile: "grpc-routing/invalid-backend.yaml",
			gwNN: types.NamespacedName{
				Namespace: "default",
				Name:      "example-gateway",
			},
			assertReports: func(gwNN types.NamespacedName, reportsMap reports.ReportMap) {
				route := &gwv1.GRPCRoute{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "example-grpc-route",
						Namespace: "default",
					},
				}
				routeStatus := reportsMap.BuildRouteStatus(context.Background(), route, wellknown.DefaultGatewayClassName)
				Expect(routeStatus).NotTo(BeNil())
				Expect(routeStatus.Parents).To(HaveLen(1))
				resolvedRefs := meta.FindStatusCondition(routeStatus.Parents[0].Conditions, string(gwv1.RouteConditionResolvedRefs))
				Expect(resolvedRefs).NotTo(BeNil())
				Expect(resolvedRefs.Status).To(Equal(metav1.ConditionFalse))
				Expect(resolvedRefs.Message).To(Equal("referencing unsupported backendRef: group \"\" kind \"ConfigMap\""))
			},
		}),
	Entry(
		"grpc gateway with multiple backend services",
		translatorTestCase{
			inputFile:  "grpc-routing/multi-backend.yaml",
			outputFile: "grpc-routing/multi-backend-proxy.yaml",
			gwNN: types.NamespacedName{
				Namespace: "default",
				Name:      "example-grpc-gateway",
			},
		}),
	Entry("Proxy with no routes", translatorTestCase{
		inputFile:  "edge-cases/no-route.yaml",
		outputFile: "no-route.yaml",
		gwNN: types.NamespacedName{
			Namespace: "default",
			Name:      "example-gateway",
		},
	}),
	Entry("HTTPRoutes with timeout and retry", translatorTestCase{
		inputFile:  "httproute-timeout-retry/manifest.yaml",
		outputFile: "httproute-timeout-retry-proxy.yaml",
		gwNN: types.NamespacedName{
			Namespace: "default",
			Name:      "example-gateway",
		},
	}),
	Entry("Service with appProtocol=anything", translatorTestCase{
		inputFile:  "backend-protocol/svc-default.yaml",
		outputFile: "backend-protocol/svc-default.yaml",
		gwNN: types.NamespacedName{
			Namespace: "default",
			Name:      "example-gateway",
		},
	}),
	Entry("Static Backend with no appProtocol", translatorTestCase{
		inputFile:  "backend-protocol/backend-default.yaml",
		outputFile: "backend-protocol/backend-default.yaml",
		gwNN: types.NamespacedName{
			Namespace: "default",
			Name:      "example-gateway",
		},
	}),
	Entry("MCP Backend with selector target", translatorTestCase{
		inputFile:  "backend-protocol/mcp-backend-selector.yaml",
		outputFile: "backend-protocol/mcp-backend-selector.yaml",
		gwNN: types.NamespacedName{
			Namespace: "default",
			Name:      "example-gateway",
		},
	}),
	Entry("MCP Backend with static target", translatorTestCase{
		inputFile:  "backend-protocol/mcp-backend-static.yaml",
		outputFile: "backend-protocol/mcp-backend-static.yaml",
		gwNN: types.NamespacedName{
			Namespace: "default",
			Name:      "example-gateway",
		},
	}),
	Entry("AI Backend with openai provider", translatorTestCase{
		inputFile:  "backend-protocol/openai-backend.yaml",
		outputFile: "backend-protocol/openai-backend.yaml",
		gwNN: types.NamespacedName{
			Namespace: "default",
			Name:      "example-gateway",
		},
	}),
	Entry("Backend with a2a provider", translatorTestCase{
		inputFile:  "backend-protocol/a2a-backend.yaml",
		outputFile: "backend-protocol/a2a-backend.yaml",
		gwNN: types.NamespacedName{
			Namespace: "default",
			Name:      "example-gateway",
		},
	}),
	Entry("AI Backend with bedrock provider", translatorTestCase{
		inputFile:  "backend-protocol/bedrock-backend.yaml",
		outputFile: "backend-protocol/bedrock-backend.yaml",

		gwNN: types.NamespacedName{
			Namespace: "default",
			Name:      "example-gateway",
		},
	}),
	Entry("Direct response", translatorTestCase{
		inputFile:  "direct-response/manifest.yaml",
		outputFile: "direct-response.yaml",

		gwNN: types.NamespacedName{
			Namespace: "default",
			Name:      "example-gateway",
		},
	}),
	Entry("DirectResponse with missing reference reports correctly", translatorTestCase{
		inputFile:  "direct-response/missing-ref.yaml",
		outputFile: "direct-response/missing-ref.yaml",
		gwNN: types.NamespacedName{
			Namespace: "default",
			Name:      "example-gateway",
		},
		assertReports: func(gwNN types.NamespacedName, reportsMap reports.ReportMap) {
			route := &gwv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "example-route",
					Namespace: "default",
				},
			}
			routeStatus := reportsMap.BuildRouteStatus(context.Background(), route, wellknown.DefaultGatewayClassName)
			Expect(routeStatus).NotTo(BeNil())
			Expect(routeStatus.Parents).To(HaveLen(1))

			// Assert ResolvedRefs=True since the route structure is valid
			resolvedRefs := meta.FindStatusCondition(routeStatus.Parents[0].Conditions, string(gwv1.RouteConditionResolvedRefs))
			Expect(resolvedRefs).NotTo(BeNil())
			Expect(resolvedRefs.Status).To(Equal(metav1.ConditionTrue))
			Expect(resolvedRefs.Reason).To(Equal(string(gwv1.RouteReasonResolvedRefs)))

			// Assert Accepted=False reports the missing DirectResponse
			acceptedCond := meta.FindStatusCondition(routeStatus.Parents[0].Conditions, string(gwv1.RouteConditionAccepted))
			Expect(acceptedCond).NotTo(BeNil())
			Expect(acceptedCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(acceptedCond.Reason).To(Equal(string(gwv1.RouteReasonBackendNotFound)))
			Expect(acceptedCond.Message).To(Equal("DirectResponse default/non-existent-ref not found"))
		},
	}),
	Entry("DirectResponse with overlapping filters reports correctly", translatorTestCase{
		inputFile:  "direct-response/overlapping-filters.yaml",
		outputFile: "direct-response/overlapping-filters.yaml",
		gwNN: types.NamespacedName{
			Namespace: "default",
			Name:      "example-gateway",
		},
		assertReports: func(gwNN types.NamespacedName, reportsMap reports.ReportMap) {
			route := &gwv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "example-route",
					Namespace: "default",
				},
			}
			routeStatus := reportsMap.BuildRouteStatus(context.Background(), route, wellknown.DefaultGatewayClassName)
			Expect(routeStatus).NotTo(BeNil())
			Expect(routeStatus.Parents).To(HaveLen(1))

			// Check for Accepted=False condition due to overlapping terminal filters
			acceptedCond := meta.FindStatusCondition(routeStatus.Parents[0].Conditions, string(gwv1.RouteConditionAccepted))
			Expect(acceptedCond).NotTo(BeNil())
			Expect(acceptedCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(acceptedCond.Reason).To(Equal(string(gwv1.RouteReasonIncompatibleFilters)))
			Expect(acceptedCond.Message).To(ContainSubstring("terminal filter"))
		},
	}),
	Entry("DirectResponse with invalid backendRef filter reports correctly", translatorTestCase{
		inputFile:  "direct-response/invalid-backendref-filter.yaml",
		outputFile: "direct-response/invalid-backendref-filter.yaml",
		gwNN: types.NamespacedName{
			Namespace: "default",
			Name:      "example-gateway",
		},
		assertReports: func(gwNN types.NamespacedName, reportsMap reports.ReportMap) {
			route := &gwv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "example-route",
					Namespace: "default",
				},
			}
			routeStatus := reportsMap.BuildRouteStatus(context.Background(), route, wellknown.DefaultGatewayClassName)
			Expect(routeStatus).NotTo(BeNil())
			Expect(routeStatus.Parents).To(HaveLen(1))

			// DirectResponse attached to backendRef should be ignored, route should resolve normally
			acceptedCond := meta.FindStatusCondition(routeStatus.Parents[0].Conditions, string(gwv1.RouteConditionAccepted))
			Expect(acceptedCond).NotTo(BeNil())
			Expect(acceptedCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(acceptedCond.Reason).To(Equal(string(gwv1.RouteReasonAccepted)))

			resolvedRefs := meta.FindStatusCondition(routeStatus.Parents[0].Conditions, string(gwv1.RouteConditionResolvedRefs))
			Expect(resolvedRefs).NotTo(BeNil())
			Expect(resolvedRefs.Status).To(Equal(metav1.ConditionTrue))
		},
	}),
	Entry("TrafficPolicy with extauth on route", translatorTestCase{
		inputFile:  "trafficpolicy/extauth-route.yaml",
		outputFile: "trafficpolicy/extauth-route.yaml",
		gwNN: types.NamespacedName{
			Namespace: "default",
			Name:      "example-gateway",
		},
	}),
	Entry("TrafficPolicy with extauth on gateway", translatorTestCase{
		inputFile:  "trafficpolicy/extauth-gateway.yaml",
		outputFile: "trafficpolicy/extauth-gateway.yaml",
		gwNN: types.NamespacedName{
			Namespace: "default",
			Name:      "example-gateway",
		},
	}),
	// TODO(npolshak): re-enable once listener policies are supported once https://github.com/agentgateway/agentgateway/pull/323 goes in
	//Entry("TrafficPolicy with extauth on listener", translatorTestCase{
	//	inputFile:  "trafficpolicy/extauth-listener.yaml",
	//	outputFile: "trafficpolicy/extauth-listener.yaml",
	//	gwNN: types.NamespacedName{
	//		Namespace: "default",
	//		Name:      "example-gateway",
	//	},
	//}),
)
