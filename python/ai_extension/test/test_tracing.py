import pytest
from opentelemetry.trace import Tracer, NoOpTracer
from telemetry.tracing import Config as TraceConfig, OtelTracer


@pytest.fixture
def tracing_config():
    return TraceConfig.from_file("test/test_data/tracing_config.b64")


@pytest.fixture
def tracing_config_direct():
    """Test fixture for direct JSON format (typical ConfigMap format)"""
    return TraceConfig.from_file("test/test_data/tracing_config_direct.json")


class TestTracing:
    def test_from_file(self, tracing_config):
        assert tracing_config.endpoint == "http://tempo.ai-test:4317"
        assert tracing_config.protocol == "grpc"
        assert tracing_config.sampler.type == "traceidratio"
        assert tracing_config.sampler.arg == 0.5  # converted from string "0.5" to float
        assert tracing_config.timeout == 100  # converted from string "100s" to int (seconds)
        assert tracing_config.transportSecurity == "insecure"

    def test_tracer_global_access(self, tracing_config):
        assert isinstance(OtelTracer.get(), NoOpTracer)

        OtelTracer.init(tracing_config.tracer())

        assert isinstance(OtelTracer.get(), Tracer)

    def test_from_file_direct_json(self, tracing_config_direct):
        """Test loading configuration from direct JSON format (typical ConfigMap)"""
        assert tracing_config_direct.endpoint == "http://tempo.ai-test:4317"
        assert tracing_config_direct.protocol == "grpc"
        assert tracing_config_direct.sampler.type == "traceidratio"
        assert tracing_config_direct.sampler.arg == 0.5  # converted from string "0.5" to float
        assert tracing_config_direct.timeout == 100  # converted from string "100s" to int (seconds)
        assert tracing_config_direct.transportSecurity == "insecure"

    def test_tracer_with_direct_json(self, tracing_config_direct):
        """Test tracer creation with direct JSON configuration"""
        tracer = tracing_config_direct.tracer()
        assert isinstance(tracer, Tracer)
        # Verify the tracer was created successfully
        assert tracer is not None
