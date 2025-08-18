package aiextension

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/kgateway-dev/kgateway/v2/pkg/utils/requestutils/curl"
	"github.com/kgateway-dev/kgateway/v2/test/gomega/matchers"
	"github.com/kgateway-dev/kgateway/v2/test/kubernetes/e2e"
	defaults "github.com/kgateway-dev/kgateway/v2/test/kubernetes/e2e/defaults"
	"github.com/kgateway-dev/kgateway/v2/test/testutils"
)

var pythonBin = func() string {
	v, ok := os.LookupEnv("PYTHON")
	if !ok {
		return "python3"
	}
	return v
}()

type tsuite struct {
	suite.Suite

	ctx context.Context

	testInst *e2e.TestInstallation

	rootDir string

	installNamespace string

	manifests map[string][]string
}

func NewSuite(
	ctx context.Context,
	testInst *e2e.TestInstallation,
) suite.TestingSuite {
	return &tsuite{
		ctx:              ctx,
		testInst:         testInst,
		rootDir:          testutils.GitRootDirectory(),
		installNamespace: os.Getenv(testutils.InstallNamespace),
	}
}

func (s *tsuite) SetupSuite() {
	s.manifests = map[string][]string{
		"TestTracing":                 {defaults.CurlPodManifest, otelCollectorManifest, tracingManifest, backendPassthroughManifest, routesBasicManifest},
		"TestRouting":                 {commonManifest, backendManifest, routesBasicManifest},
		"TestRoutingPassthrough":      {commonManifest, backendPassthroughManifest, routesBasicManifest},
		"TestRoutingOverrideProvider": {commonManifest, backendPassthroughManifest, routesBasicManifest},
		"TestStreaming":               {commonManifest, backendManifest, routeOptionStreamingManifest, routesWithExtensionManifest},
		"TestPromptGuardRejectExtRef": {commonManifest, backendManifest, trafficPolicyPGRegexPatternRejectManifest, routesWitPGRegexPatternRejectManifest},
		"TestPromptGuard":             {commonManifest, backendManifest, routesBasicManifest, promptGuardManifest},
		"TestPromptGuardStreaming":    {commonManifest, backendManifest, routesBasicManifest, promptGuardStreamingManifest},
	}
}

func (s *tsuite) TearDownSuite() {
}

func (s *tsuite) waitForEnvoyReady() {
	gwURL := s.getGatewayURL()
	fmt.Println("Waiting for envoy up.")
	s.Require().EventuallyWithT(func(c *assert.CollectT) {
		statusChar := "."
		resp, err := http.Get(gwURL + "/not_there")
		if err == nil {
			defer resp.Body.Close()
			statusChar = "*"
			assert.Equalf(c, resp.StatusCode, 404, "envoy up check failed")
		}
		fmt.Print(statusChar)
	}, 30*time.Second, 1*time.Second)
	fmt.Println()
}

func (s *tsuite) BeforeTest(suiteName, testName string) {
	manifests := s.manifests[testName]
	fmt.Printf("Applying manifests for test %s in suite %s", testName, suiteName)
	for _, manifest := range manifests {
		err := s.testInst.Actions.Kubectl().ApplyFile(s.ctx, manifest)
		s.Require().NoError(err)
	}

	s.waitForEnvoyReady()
}

func (s *tsuite) AfterTest(suiteName, testName string) {
	if s.T().Failed() {
		s.testInst.PreFailHandler(s.ctx)
	}
	manifests := s.manifests[testName]
	fmt.Printf("Deleting manifests for test %s in suite %s", testName, suiteName)
	for _, manifest := range manifests {
		err := s.testInst.Actions.Kubectl().DeleteFileSafe(s.ctx, manifest)
		s.Require().NoError(err)
	}
}

func (s *tsuite) TestTracing() {
	tracingConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ai-gateway",
			Namespace: s.testInst.Metadata.InstallNamespace,
		},
	}

	s.testInst.Assertions.EventuallyObjectsExist(s.ctx, tracingConfig)

	s.Require().EventuallyWithT(func(c *assert.CollectT) {
		err := s.testInst.ClusterContext.Client.Get(
			s.ctx,
			types.NamespacedName{Name: tracingConfig.Name, Namespace: tracingConfig.Namespace},
			tracingConfig,
		)
		assert.NoErrorf(c, err, "failed to get configMap %s/%s", tracingConfig.Namespace, tracingConfig.Name)
	}, 30*time.Second, 1*time.Second)

	s.waitForOTelCollectorReady()

	// Run OTel tracing span validation test
	s.testOTelSpan()
}

func (s *tsuite) waitForOTelCollectorReady() {
	otelCollectorPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "otel-collector",
			Namespace: s.testInst.Metadata.InstallNamespace,
		},
	}

	s.testInst.Assertions.EventuallyObjectsExist(s.ctx, otelCollectorPod)

	s.Require().EventuallyWithT(func(c *assert.CollectT) {
		err := s.testInst.ClusterContext.Client.Get(
			s.ctx,
			types.NamespacedName{Name: otelCollectorPod.Name, Namespace: otelCollectorPod.Namespace},
			otelCollectorPod,
		)
		assert.NoErrorf(c, err, "failed to get pod %s/%s", otelCollectorPod.Namespace, otelCollectorPod.Name)

		// Check if the Pod is in Running state
		assert.Equalf(c, corev1.PodRunning, otelCollectorPod.Status.Phase,
			"pod %s/%s is not running, current phase: %s",
			otelCollectorPod.Namespace, otelCollectorPod.Name, otelCollectorPod.Status.Phase)

		// Check if the otel-collector container is running and ready
		otelCollectorFound := false
		for _, containerStatus := range otelCollectorPod.Status.ContainerStatuses {
			if containerStatus.Name == "otel-collector" {
				otelCollectorFound = true

				// Check if the container is running
				assert.NotNilf(c, containerStatus.State.Running,
					"otel-collector container is not running, current state: %+v",
					containerStatus.State)

				// Check if the container is ready
				assert.Truef(c, containerStatus.Ready,
					"otel-collector container is not ready")

				break
			}
		}

		assert.Truef(c, otelCollectorFound,
			"otel-collector container not found in pod %s/%s",
			otelCollectorPod.Namespace, otelCollectorPod.Name)
	}, 60*time.Second, 5*time.Second)
}

func (s *tsuite) testOTelSpan() {
	// Wait until the tracing policy is accepted by the Gateway
	s.testInst.Assertions.EventuallyHTTPListenerPolicyCondition(s.ctx, "tracing-policy", s.installNamespace, gwv1.GatewayConditionAccepted, metav1.ConditionTrue)

	var mockLLMProviders = []struct {
		name         string
		auth         []string
		path         string
		body         string
		exceptedLogs [][]string
	}{
		{
			name: "openai",
			auth: []string{"Authorization", "Bearer passthrough-openai-key"},
			path: "/openai",
			body: s.getOpenAIChatRequestPayload(),
			// {"level":"info","ts":"2025-08-18T10:32:36.011Z","msg":"ResourceSpans #0\nResource SchemaURL: \nResource attributes:\n     -> telemetry.sdk.language: Str(python)\n     -> telemetry.sdk.name: Str(opentelemetry)\n     -> telemetry.sdk.version: Str(1.35.0)\n     -> service.name: Str(kgateway-ai-extension)\nScopeSpans #0\nScopeSpans SchemaURL: \nInstrumentationScope telemetry.tracing \nSpan #0\n    Trace ID       : 5e0724091ed15094c1f74b08e7f5ecda\n    Parent ID      : ebab791fbe9bccaf\n    ID             : e4baff58f44eebe3\n    Name           : parse_config\n    Kind           : Internal\n    Start time     : 2025-08-18 10:32:34.177490154 +0000 UTC\n    End time       : 2025-08-18 10:32:34.177528917 +0000 UTC\n    Status code    : Unset\n    Status message : \nSpan #1\n    Trace ID       : 5e0724091ed15094c1f74b08e7f5ecda\n    Parent ID      : 104c310bb827fee9\n    ID             : ebab791fbe9bccaf\n    Name           : handle_request_headers\n    Kind           : Internal\n    Start time     : 2025-08-18 10:32:34.177233032 +0000 UTC\n    End time       : 2025-08-18 10:32:34.178327305 +0000 UTC\n    Status code    : Unset\n    Status message : \nSpan #2\n    Trace ID       : 5e0724091ed15094c1f74b08e7f5ecda\n    Parent ID      : ec8a665ea29a0d01\n    ID             : 2d43f5108d2c76ce\n    Name           : gen_ai.request generate_content gpt-4o-mini\n    Kind           : Internal\n    Start time     : 2025-08-18 10:32:34.17893748 +0000 UTC\n    End time       : 2025-08-18 10:32:34.384914245 +0000 UTC\n    Status code    : Ok\n    Status message : \nAttributes:\n     -> gen_ai.output.type: Str(text)\n     -> gen_ai.request.choice.count: Int(2)\n     -> gen_ai.request.model: Str(gpt-4o-mini)\n     -> gen_ai.request.seed: Int(12345)\n     -> gen_ai.request.frequency_penalty: Double(0.5)\n     -> gen_ai.request.max_tokens: Int(150)\n     -> gen_ai.request.presence_penalty: Double(0.3)\n     -> gen_ai.request.stop_sequences: Slice([\"\\n\\n\",\"END\"])\n     -> gen_ai.request.temperature: Double(0.7)\n     -> gen_ai.request.top_k: Int(0)\n     -> gen_ai.request.top_p: Double(0.9)\n     -> gen_ai.operation.name: Str(generate_content)\n     -> gen_ai.system: Str(openai)\nSpan #3\n    Trace ID       : 5e0724091ed15094c1f74b08e7f5ecda\n    Parent ID      : 104c310bb827fee9\n    ID             : ec8a665ea29a0d01\n    Name           : handle_request_body\n    Kind           : Internal\n    Start time     : 2025-08-18 10:32:34.178627899 +0000 UTC\n    End time       : 2025-08-18 10:32:34.385393273 +0000 UTC\n    Status code    : Unset\n    Status message : \nSpan #4\n    Trace ID       : 5e0724091ed15094c1f74b08e7f5ecda\n    Parent ID      : 104c310bb827fee9\n    ID             : d368a7fa273e8881\n    Name           : handle_response_headers\n    Kind           : Internal\n    Start time     : 2025-08-18 10:32:34.396281546 +0000 UTC\n    End time       : 2025-08-18 10:32:34.396669183 +0000 UTC\n    Status code    : Unset\n    Status message : \nSpan #5\n    Trace ID       : 5e0724091ed15094c1f74b08e7f5ecda\n    Parent ID      : 30577fc6934875d5\n    ID             : 4807c512cb668a5c\n    Name           : gen_ai.response\n    Kind           : Internal\n    Start time     : 2025-08-18 10:32:34.397609908 +0000 UTC\n    End time       : 2025-08-18 10:32:34.399312965 +0000 UTC\n    Status code    : Unset\n    Status message : \nAttributes:\n     -> gen_ai.response.id: Str(chatcmpl-B8Vy5kfL1Wc9LPp6K28Ot4MwDsQ83)\n     -> gen_ai.response.model: Str(gpt-4o-mini-2024-07-18)\n     -> gen_ai.response.finish_reasons: Str(stop)\n     -> gen_ai.usage.input_tokens: Int(39)\n     -> gen_ai.usage.output_tokens: Int(333)\n     -> gen_ai.operation.name: Str(generate_content)\n     -> gen_ai.system: Str(openai)\nSpan #6\n    Trace ID       : 5e0724091ed15094c1f74b08e7f5ecda\n    Parent ID      : 104c310bb827fee9\n    ID             : 30577fc6934875d5\n    Name           : handle_response_body\n    Kind           : Internal\n    Start time     : 2025-08-18 10:32:34.396936906 +0000 UTC\n    End time       : 2025-08-18 10:32:34.39966222 +0000 UTC\n    Status code    : Unset\n    Status message : \n","kind":"exporter","data_type":"traces","name":"debug"}
			exceptedLogs: [][]string{
				{
					`gen_ai.request generate_content gpt-4o-mini`,
					`-> gen_ai.output.type: Str(text)`,
					`-> gen_ai.request.choice.count: Int(2)`,
					`-> gen_ai.request.model: Str(gpt-4o-mini)`,
					`-> gen_ai.request.seed: Int(12345)`,
					`-> gen_ai.request.frequency_penalty: Double(0.5)`,
					`-> gen_ai.request.max_tokens: Int(150)`,
					`-> gen_ai.request.presence_penalty: Double(0.3)`,
					`-> gen_ai.request.stop_sequences: Slice([\"\\n\\n\",\"END\"])`,
					`-> gen_ai.request.temperature: Double(0.7)`,
					`-> gen_ai.request.top_k: Int(0)`,
					`-> gen_ai.request.top_p: Double(0.9)`,
					`-> gen_ai.operation.name: Str(generate_content)`,
					`-> gen_ai.system: Str(openai)`,
				},
				{
					`gen_ai.response`,
					`-> gen_ai.response.id: Str(chatcmpl-B8Vy5kfL1Wc9LPp6K28Ot4MwDsQ83)`,
					`-> gen_ai.response.model: Str(gpt-4o-mini-2024-07-18)`,
					`-> gen_ai.response.finish_reasons: Str(stop)`,
					`-> gen_ai.usage.input_tokens: Int(39)`,
					`-> gen_ai.usage.output_tokens: Int(333)`,
					`-> gen_ai.operation.name: Str(generate_content)`,
					`-> gen_ai.system: Str(openai)`,
				},
			},
		},
	}

	for _, provider := range mockLLMProviders {
		// Send a test request to the AI gateway and verify HTTP response.
		// This triggers the OTel span generation in the backend.
		s.testInst.Assertions.AssertEventualCurlResponse(
			s.ctx,
			defaults.CurlPodExecOpt,
			[]curl.Option{
				curl.WithHost(s.getGatewayIP()),
				curl.WithPort(int(s.getGatewayPort())),
				curl.WithHeader(provider.auth[0], provider.auth[1]),
				curl.WithPath(provider.path),
				curl.WithBody(s.getOpenAIChatRequestPayload()),
			},
			&matchers.HttpResponse{
				StatusCode: http.StatusOK,
			},
			20*time.Second,
			2*time.Second,
		)

		// Periodically fetch OTel collector pod logs and check for expected span logs.
		// This ensures that the spans are actually exported and visible in the logs.
		s.Require().EventuallyWithT(func(c *assert.CollectT) {
			logs, err := s.testInst.Actions.Kubectl().GetContainerLogs(s.ctx, s.testInst.Metadata.InstallNamespace, "otel-collector")
			s.Require().NoError(err)
			for _, expectedSpan := range provider.exceptedLogs {
				s.assertSpanLogsPresent(logs, expectedSpan)
			}
		}, 60*time.Second, 15*time.Second)
	}
}

// assertSpanLogsPresent checks if all expected span log entries are present in the OTel collector logs.
// If any expected log is missing, the test will fail and print the missing entries.
func (s *tsuite) assertSpanLogsPresent(otelLogs string, expectedSpanEntries []string) {
	allPresent := true
	var missingEntries []string
	for _, entry := range expectedSpanEntries {
		if !strings.Contains(otelLogs, entry) {
			allPresent = false
			missingEntries = append(missingEntries, entry)
		}
	}
	// Fail the test if any expected log entry is missing, and print missing entries for debugging
	s.Assertions.True(allPresent, fmt.Sprintf("OTel span logs missing: %v", missingEntries))
}

// More descriptive implementation function, returns the request body sent to AI gateway / OpenAI
func (s *tsuite) getOpenAIChatRequestPayload() string {
	return `
	{
		"model": "gpt-4o-mini",
		"messages": [
			{
				"role": "system",
				"content": "You are a poetic assistant, skilled in explaining complex programming concepts with creative flair."
			},
			{
				"role": "user",
				"content": "Compose a poem that explains the concept of recursion in programming."
			}
		],
		"response_format": {"type": "text"},
		"n": 2,
		"seed": 12345,
		"frequency_penalty": 0.5,
		"max_tokens": 150,
		"presence_penalty": 0.3,
		"stop": ["\n\n", "END"],
		"temperature": 0.7,
		"top_p": 0.9
	}`
}

func (s *tsuite) TestRouting() {
	s.invokePytest("routing.py")
}

func (s *tsuite) TestRoutingPassthrough() {
	s.invokePytest(
		"routing.py",
		"TEST_TOKEN_PASSTHROUGH=true",
	)
}

func (s *tsuite) TestRoutingOverrideProvider() {
	s.invokePytest(
		"routing.py",
		"TEST_OVERRIDE_PROVIDER=true",
	)
}

func (s *tsuite) TestStreaming() {
	s.invokePytest("streaming.py")
}

func (s *tsuite) TestPromptGuardRejectExtRef() {
	s.invokePytest("prompt_guard_reject_ext_ref.py")
}

func (s *tsuite) TestPromptGuard() {
	s.invokePytest("prompt_guard.py")
}

func (s *tsuite) TestPromptGuardStreaming() {
	s.invokePytest("prompt_guard_streaming.py")
}

func (s *tsuite) invokePytest(test string, extraEnv ...string) {
	fmt.Printf("Using Python binary: %s\n", pythonBin)

	gwURL := s.getGatewayURL()
	logLevel := os.Getenv("TEST_PYTHON_LOG_LEVEL")
	if logLevel == "" {
		logLevel = "DEBUG"
	}

	args := []string{"-m", "pytest", test, "-vvv", "--log-cli-level=" + logLevel}
	if pyMatch := os.Getenv("TEST_PYTHON_STRING_MATCH"); pyMatch != "" {
		args = append(args, "-k="+pyMatch)
	}

	cmd := exec.Command(pythonBin, args...)
	cmd.Dir = filepath.Join(s.rootDir, "test/kubernetes/e2e/features/aiextension/tests")
	cmd.Env = []string{
		fmt.Sprintf("TEST_OVERRIDE_BASE_URL=%s/openai-override", gwURL),
		fmt.Sprintf("TEST_OPENAI_BASE_URL=%s/openai", gwURL),
		fmt.Sprintf("TEST_AZURE_OPENAI_BASE_URL=%s/azure", gwURL),
		fmt.Sprintf("TEST_GEMINI_BASE_URL=%s/gemini", gwURL), // need to specify HTTP as part of the endpoint
		fmt.Sprintf("TEST_VERTEX_AI_BASE_URL=%s/vertex-ai", gwURL),
		fmt.Sprintf("TEST_GATEWAY_ADDRESS=%s", gwURL),
	}
	cmd.Env = append(cmd.Env, extraEnv...)

	fmt.Printf("Running Test Command: %s\n", cmd.String())
	fmt.Printf("Using Environment Values: %v\n", cmd.Env)

	out, err := cmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// Check the exit code
			if exitErr.ExitCode() == 5 {
				// When all tests are filtered (by TEST_PYTHON_STRING_MATCH), pytest returns exit code 5
				// ignore it
			} else {
				s.Require().NoError(err, string(out))
			}
		}
	}
	s.T().Logf("Test output: %s", string(out))
}

// getGatewayService gets the ai-gateway Service object, waits for it to be ready, and returns it
func (s *tsuite) getGatewayService() *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ai-gateway",
			Namespace: s.testInst.Metadata.InstallNamespace,
		},
	}
	s.testInst.Assertions.EventuallyObjectsExist(s.ctx, svc)

	s.Require().Greater(len(svc.Spec.Ports), 0)

	s.Require().EventuallyWithT(func(c *assert.CollectT) {
		err := s.testInst.ClusterContext.Client.Get(
			s.ctx,
			types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace},
			svc,
		)
		assert.NoErrorf(c, err, "failed to get service %s/%s", svc.Namespace, svc.Name)
		assert.Greaterf(c, len(svc.Status.LoadBalancer.Ingress), 0, "LB IP not found on service %s/%s", svc.Namespace, svc.Name)
	}, 10*time.Second, 1*time.Second)

	return svc
}

func (s *tsuite) getGatewayURL() string {
	svc := s.getGatewayService()
	return fmt.Sprintf("http://%s:%d", svc.Status.LoadBalancer.Ingress[0].IP, svc.Spec.Ports[0].Port)
}

func (s *tsuite) getGatewayIP() string {
	svc := s.getGatewayService()
	return svc.Status.LoadBalancer.Ingress[0].IP
}

func (s *tsuite) getGatewayPort() int32 {
	svc := s.getGatewayService()
	return svc.Spec.Ports[0].Port
}
