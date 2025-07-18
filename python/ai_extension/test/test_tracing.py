import pytest
from opentelemetry.trace import Tracer, NoOpTracer
from opentelemetry.sdk.trace.sampling import TraceIdRatioBased
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from telemetry.tracing import Config as TraceConfig, OtelTracer


@pytest.fixture
def tracing_config():
    """Test fixture for JSON format ConfigMap (standard format)"""
    return TraceConfig.from_file("test/test_data/tracing_config.json")

@pytest.fixture(autouse=True)
def reset_tracer():
    """Reset the OtelTracer state before each test"""
    OtelTracer._OtelTracer__tracer = None
    yield
    # Reset after test to ensure no state leakage
    OtelTracer._OtelTracer__tracer = None
    
class TestTracing:
    def test_from_file(self, tracing_config):
        assert tracing_config.endpoint == "http://tempo.ai-test:4317"
        assert tracing_config.protocol == "grpc"
        assert tracing_config.sampler.type == "traceidratio"
        assert tracing_config.sampler.arg == 0.5  # converted from string "0.5" to float
        assert (
            tracing_config.timeout == 100
        )  # converted from string "100s" to int (seconds)
        assert tracing_config.transportSecurity == "insecure"

    def test_tracer_global_access(self, tracing_config):
        assert isinstance(OtelTracer.get(), NoOpTracer)

        OtelTracer.init(tracing_config.tracer())

        assert isinstance(OtelTracer.get(), Tracer)

    def test_tracer_creation(self, tracing_config):
        """Test tracer creation with JSON configuration and verify all components are configured correctly"""
        tracer = tracing_config.tracer()
        assert isinstance(tracer, Tracer)
        assert tracer is not None

        # Verify the resource contains the expected service name
        resource = tracer.resource
        assert resource.attributes.get("service.name") == "kgateway-ai-extension"

        # Verify sampler configuration
        sampler = tracer.sampler
        assert isinstance(sampler, TraceIdRatioBased)
        assert sampler._rate == 0.5

        # Verify span processor and exporter configuration
        self._verify_span_processor_configuration(tracer, tracing_config)

    def test_sampler_creation(self, tracing_config):
        """Test that different sampler types are created correctly"""
        # Test the current configuration (traceidratio)
        sampler = tracing_config._create_sampler()
        assert isinstance(sampler, TraceIdRatioBased)
        assert sampler._rate == 0.5

    def test_exporter_creation(self, tracing_config):
        """Test that exporter is created with correct configuration"""
        exporter = tracing_config._create_exporter()
        assert isinstance(exporter, OTLPSpanExporter)
        self._assert_exporter_configuration(exporter, tracing_config.endpoint, 100)

    @pytest.mark.parametrize(
        "sampler_type,sampler_arg,expected_type,expected_value",
        [
            ("always_on", None, "static", "ALWAYS_ON"),
            ("always_off", None, "static", "ALWAYS_OFF"),
            ("parentbased_always_on", None, "static", "DEFAULT_ON"),
            ("parentbased_always_off", None, "static", "DEFAULT_OFF"),
            ("traceidratio", 0.25, "ratio", 0.25),
            ("traceidratio", 0.75, "ratio", 0.75),
        ],
    )
    def test_different_sampler_types(
        self, sampler_type, sampler_arg, expected_type, expected_value
    ):
        """Test that different sampler types are created correctly"""
        from opentelemetry.sdk.trace.sampling import (
            ALWAYS_ON,
            ALWAYS_OFF,
            DEFAULT_ON,
            DEFAULT_OFF,
        )

        sampler_config = {"type": sampler_type}
        if sampler_arg is not None:
            sampler_config["arg"] = sampler_arg

        config = self._create_test_config(sampler=sampler_config)
        sampler = config._create_sampler()

        if expected_type == "static":
            expected_sampler = {
                "ALWAYS_ON": ALWAYS_ON,
                "ALWAYS_OFF": ALWAYS_OFF,
                "DEFAULT_ON": DEFAULT_ON,
                "DEFAULT_OFF": DEFAULT_OFF,
            }[expected_value]
            assert sampler == expected_sampler
        elif expected_type == "ratio":
            assert isinstance(sampler, TraceIdRatioBased)
            assert sampler._rate == expected_value

    @pytest.mark.parametrize(
        "timeout_input,expected_seconds",
        [
            ("30s", 30),
            ("5m", 300),  # 5 * 60
            ("2h", 7200),  # 2 * 60 * 60
            ("120", 120),  # plain string number
            (60, 60),  # integer
        ],
    )
    def test_timeout_parsing(self, timeout_input, expected_seconds):
        """Test that timeout strings are parsed correctly"""
        config = self._create_test_config(timeout=timeout_input)
        assert config.timeout == expected_seconds

    @pytest.mark.parametrize(
        "protocol,endpoint,expected_exporter_type",
        [
            ("grpc", "http://test:4317", "grpc"),
            ("http", "http://test:4318/v1/traces", "http"),
            ("http/protobuf", "http://test:4318/v1/traces", "http"),
        ],
    )
    def test_protocol_support(self, protocol, endpoint, expected_exporter_type):
        """Test that different protocols create appropriate exporters"""
        from opentelemetry.exporter.otlp.proto.http.trace_exporter import (
            OTLPSpanExporter as HTTPExporter,
        )

        config = self._create_test_config(endpoint=endpoint, protocol=protocol)
        exporter = config._create_exporter()

        if expected_exporter_type == "grpc":
            assert isinstance(exporter, OTLPSpanExporter)  # gRPC exporter
        else:  # http
            assert isinstance(exporter, HTTPExporter)  # HTTP exporter

    # Helper methods
    def _create_test_config(self, **overrides):
        """Helper method to create test configuration with overrides"""
        base_config = {"endpoint": "http://test:4317"}
        base_config.update(overrides)
        return TraceConfig(**base_config)

    def _assert_exporter_configuration(
        self, exporter, expected_endpoint, expected_timeout
    ):
        """Helper method to assert exporter configuration"""
        # gRPC exporter strips protocol from endpoint
        expected_clean_endpoint = expected_endpoint.replace("http://", "").replace(
            "https://", ""
        )
        assert exporter._endpoint == expected_clean_endpoint
        assert exporter._timeout == expected_timeout

    def _verify_span_processor_configuration(self, tracer, config):
        """Helper method to verify span processor configuration"""
        span_processor = tracer.span_processor
        assert span_processor is not None

        # Handle both composite and single processor cases
        processors = (
            span_processor._span_processors
            if hasattr(span_processor, "_span_processors")
            else [span_processor]
        )

        # Find BatchSpanProcessor
        batch_processor = next(
            (p for p in processors if isinstance(p, BatchSpanProcessor)), None
        )
        assert batch_processor is not None, "BatchSpanProcessor should be configured"

        # Verify exporter
        exporter = batch_processor.span_exporter
        assert isinstance(exporter, OTLPSpanExporter)
        self._assert_exporter_configuration(exporter, config.endpoint, config.timeout)
