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

	err := s.testInst.Actions.Kubectl().ApplyFile(s.ctx, tracingPolicyManifest)
	s.Require().NoError(err)

	s.testOTelRequestSpan()
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

		// 检查 Pod 是否处于 Running 状态
		assert.Equalf(c, corev1.PodRunning, otelCollectorPod.Status.Phase,
			"pod %s/%s is not running, current phase: %s",
			otelCollectorPod.Namespace, otelCollectorPod.Name, otelCollectorPod.Status.Phase)

		// 检查 otel-collector 容器是否正在运行且就绪
		otelCollectorFound := false
		for _, containerStatus := range otelCollectorPod.Status.ContainerStatuses {
			if containerStatus.Name == "otel-collector" {
				otelCollectorFound = true

				// 检查容器是否在运行
				assert.NotNilf(c, containerStatus.State.Running,
					"otel-collector container is not running, current state: %+v",
					containerStatus.State)

				// 检查容器是否就绪
				assert.Truef(c, containerStatus.Ready,
					"otel-collector container is not ready")

				break
			}
		}

		assert.Truef(c, otelCollectorFound,
			"otel-collector container not found in pod %s/%s",
			otelCollectorPod.Namespace, otelCollectorPod.Name)
	}, 60*time.Second, 2*time.Second)
}

func (s *tsuite) testOTelRequestSpan() {
	s.testInst.Assertions.EventuallyHTTPListenerPolicyCondition(s.ctx, "tracing-policy", s.installNamespace, gwv1.GatewayConditionAccepted, metav1.ConditionTrue)

	s.testInst.Assertions.AssertEventualCurlResponse(
		s.ctx,
		defaults.CurlPodExecOpt,
		[]curl.Option{
			curl.WithHost(s.getGatewayIP()),
			curl.WithPort(int(s.getGatewayPort())),
			curl.WithHeader("Authorization", "Bearer passthrough-openai-key"),
			curl.WithPath("/openai"),
			curl.WithBody(s.getOpenAIChatRequestPayload()),
		},
		&matchers.HttpResponse{
			StatusCode: http.StatusOK,
		},
		20*time.Second,
		2*time.Second,
	)

	requestSpanLogs := []string{
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
	}

	// fetch the collector pod logs
	s.Require().EventuallyWithT(func(c *assert.CollectT) {
		logs, err := s.testInst.Actions.Kubectl().GetContainerLogs(s.ctx, s.testInst.Metadata.InstallNamespace, "otel-collector")
		s.Require().NoError(err)

		fmt.Printf("%s", logs)

		allMatched := true
		var missedMsgs []string
		for _, log := range requestSpanLogs {
			if !strings.Contains(logs, log) {
				allMatched = false
				missedMsgs = append(missedMsgs, log)
			}
		}

		s.Assertions.True(allMatched, fmt.Sprintf("miss excpeted logs: %s", missedMsgs))
	}, 60*time.Second, 2*time.Second)
}

// 更具描述性的实现函数，返回发送给 AI gateway / OpenAI 的请求体
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

// getGatewayService 获取 ai-gateway 服务对象，等待其就绪并返回
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
