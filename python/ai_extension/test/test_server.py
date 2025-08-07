import asyncio
import json
import logging

import pytest

from guardrails.presidio import init_presidio_config
from python.ai_extension.api.kgateway.policy.ai import prompt_guard
import telemetry.attributes as ai_attributes
from telemetry.stats import Config as StatsConfig
from telemetry.tracing import Config as TracingConfig, OtelTracer
from ext_proc.server import StreamHandler, ExtProcServer
from api.envoy.service.ext_proc.v3 import external_processor_pb2
from prometheus_client import CollectorRegistry, Counter
from openai.types.moderation_create_response import ModerationCreateResponse
from openai.types.moderation import (
    Moderation,
    Categories,
    CategoryScores,
    CategoryAppliedInputTypes,
)

from openai.resources import AsyncModerations
from openai import AsyncOpenAI
from opentelemetry import trace
from opentelemetry.semconv._incubating.attributes import gen_ai_attributes
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.trace import set_tracer_provider


class MockAsyncOpenAI(AsyncOpenAI):
    def __init__(self, client):
        pass


class MockModerationClient(AsyncModerations):
    async def create(self, **params):
        response = ModerationCreateResponse(
            id="test-id",
            model="test-model",
            results=[
                Moderation(
                    flagged=True,
                    categories=Categories.construct(),
                    category_scores=CategoryScores.construct(),
                    category_applied_input_types=CategoryAppliedInputTypes.construct(),
                )
            ],
        )
        return response


logger = logging.getLogger().getChild("server-test-logger")

# Create a new CollectorRegistry to ensure no previous collectors are registered
test_registry = CollectorRegistry()

# Example of creating a Counter with the new registry
ai_prompt_tokens = Counter("ai_prompt_tokens", "Description", registry=test_registry)

stats_config = StatsConfig(customLabels=[])
tracing_config = TracingConfig()
extproc_server = ExtProcServer(stats_config, tracing_config)


@pytest.mark.parametrize(
    "req_body_content, llm_provider",
    [
        (
            b"""{
  "model": "gpt-4o-mini",
  "messages": [
    {
      "role": "user",
      "content": "Are you ok?"
    }
  ],
  "stream": false
}""",
            "openai",
        ),
        (
            b"""{
  "contents":[
     {
        "role":"user",
        "parts":[
           {
              "text":"Gemini is cool!"
           }
        ]
     }
  ]
}""",
            "gemini",
        ),
    ],
)
def test_handle_request_body(req_body_content, llm_provider):
    req_body = external_processor_pb2.HttpBody(
        body=req_body_content,
        end_of_stream=True,
    )
    metadict = {"x-llm-provider": llm_provider}

    handler = StreamHandler.from_metadata(metadict)
    handler.req_webhook = None
    handler.req.set_headers(
        headers=external_processor_pb2.HttpHeaders(), header_rules=[]
    )

    response = asyncio.run(
        extproc_server.handle_request_body(
            req_body,
            metadict,
            handler,
            trace.NonRecordingSpan(trace.SpanContext(0, 0, False)),
        )
    )

    assert response is not None
    assert isinstance(response, external_processor_pb2.ProcessingResponse)
    assert json.loads(
        response.request_body.response.body_mutation.body.decode("utf-8")
    ) == json.loads(req_body.body.decode("utf-8"))
    assert "envoy.ratelimit" in response.dynamic_metadata.fields
    ratelimit = response.dynamic_metadata.fields["envoy.ratelimit"].struct_value
    assert "hits_addend" in ratelimit.fields


def test_handle_request_headers():
    headers = external_processor_pb2.HttpHeaders()
    headers.headers.headers.add(key="authorization", value="test-auth-token")

    metadict = {"x-llm-provider": "openai"}
    handler = StreamHandler.from_metadata(metadict)
    handler.req.headers = {
        header.key: header.value for header in headers.headers.headers
    }

    response = extproc_server.handle_request_headers(headers, handler)
    assert "ai.kgateway.io" in response.dynamic_metadata.fields
    ai_metadata = response.dynamic_metadata.fields["ai.kgateway.io"].struct_value
    assert "auth_token" in ai_metadata.fields


@pytest.mark.parametrize(
    "req_body_content, llm_provider",
    [
        (
            b"""{
  "model": "gpt-4o-mini",
  "messages": [
    {
      "role": "user",
      "content": "Are you ok?"
    }
  ],
  "stream": false
}""",
            "openai",
        ),
        (
            b"""{
  "contents":[
     {
        "role":"user",
        "parts":[
           {
              "text":"Gemini is cool!"
           }
        ]
     }
  ]
}""",
            "gemini",
        ),
    ],
)
def test_handle_request_body_with_moderation(req_body_content, llm_provider):
    req_body = external_processor_pb2.HttpBody(
        body=req_body_content,
        end_of_stream=True,
    )
    metadict = {"x-llm-provider": llm_provider}

    handler = StreamHandler.from_metadata(metadict)
    handler.req_webhook = None
    handler.req.set_headers(
        headers=external_processor_pb2.HttpHeaders(), header_rules=[]
    )
    handler.req_moderation = (
        MockModerationClient(client=MockAsyncOpenAI(client=None)),
        "test-model",
    )
    response = asyncio.run(
        extproc_server.handle_request_body(
            req_body,
            metadict,
            handler,
            trace.NonRecordingSpan(trace.SpanContext(0, 0, False)),
        )
    )

    assert response.immediate_response.body == b"Rejected by guardrails moderation"


@pytest.mark.parametrize(
    "req_body_content, resp_body_content, llm_provider",
    [
        (
            b"""{
  "model": "gpt-4o-mini",
  "messages": [
    {
      "role": "user",
      "content": "Are you ok?"
    }
  ],
  "stream": false
}""",
            """{
   "id":"fake",
   "object":"chat.completion",
   "created":1722966273,
   "model":"gpt-4o-mini-2024-07-18",
   "choices":[
      {
         "index":0,
         "message":{
            "role":"assistant",
            "content":"Say hello to the world!",
            "refusal":null
         },
         "logprobs":null,
         "finish_reason":"stop"
      }
   ],
   "usage":{
      "prompt_tokens":11,
      "completion_tokens":310,
      "total_tokens":321
   },
   "system_fingerprint":"fp_48196bc67a"
}""",
            "openai",
        ),
        (
            b"""{
  "contents":[
     {
        "role":"user",
        "parts":[
           {
              "text":"Gemini is cool!"
           }
        ]
     }
  ]
}""",
            """{
  "candidates":[
     {
        "content":{
           "role":"model",
           "parts":[
              {
                 "text":"Say hello to the world!"
              }
           ]
        }
     }
  ],
  "modelVersion":"gemini-1.5-flash-001",
  "usageMetadata": {
    "promptTokenCount": 4,
    "candidatesTokenCount": 491,
    "totalTokenCount": 495
  }
}""",
            "gemini",
        ),
    ],
)
def test_handle_response_body_with_end_of_stream(
    req_body_content, resp_body_content, llm_provider
):
    headers = external_processor_pb2.HttpHeaders()
    headers.headers.headers.add(key="authorization", value="test-auth-token")

    metadict = {"x-llm-provider": llm_provider}
    handler = StreamHandler.from_metadata(metadict)
    handler.req.headers = {
        header.key: header.value for header in headers.headers.headers
    }

    handler.req.append(req_body_content)

    resp_body = external_processor_pb2.HttpBody(
        body=resp_body_content.encode("utf-8"), end_of_stream=True
    )
    response = asyncio.run(
        extproc_server.handle_response_body(
            resp_body, handler, trace.NonRecordingSpan(trace.SpanContext(0, 0, False))
        )
    )

    assert response is not None
    assert isinstance(response, external_processor_pb2.ProcessingResponse)
    assert json.loads(
        response.response_body.response.body_mutation.body.decode("utf-8")
    ) == json.loads(resp_body_content)


@pytest.fixture
def setup_in_memory_tracer():
    """Setup in-memory span exporter for testing instrumentation"""

    # Create in-memory exporter
    memory_exporter = InMemorySpanExporter()

    # Create tracer provider with in-memory exporter
    tracer_provider = TracerProvider()
    tracer_provider.add_span_processor(SimpleSpanProcessor(memory_exporter))

    # Set as global tracer provider
    set_tracer_provider(tracer_provider)

    # Force override the existing tracer (bypassing init protection logic)
    test_tracer = tracer_provider.get_tracer("test-tracer")
    OtelTracer._OtelTracer__tracer = test_tracer

    # Verify override success
    current_tracer = OtelTracer.get()
    assert current_tracer is test_tracer, "Failed to override tracer"

    yield memory_exporter, test_tracer

    memory_exporter.clear()


def stream_handler(metadict: dict) -> StreamHandler:
    """
    Create a common StreamHandler instance for testing.

    Args:
        metadict: Metadata dictionary containing LLM provider info

    Returns:
        Configured StreamHandler instance
    """
    headers = external_processor_pb2.HttpHeaders()

    handler = StreamHandler.from_metadata(metadict)
    handler.req.set_headers(headers=headers, header_rules=[])
    handler.resp.set_headers(headers=headers, header_rules=[])
    return handler


class TestInstrumentation:
    @pytest.fixture(scope="package")
    def request_body_content(self):
        """Fixture for common request body content."""
        return {
            "model": "openai/gpt-4o",
            "messages": [
                {
                    "role": "system",
                    "content": "You are a helpful assistant that answers questions concisely.",
                },
                {
                    "role": "user",
                    "content": "What is the meaning of life? Please elaborate in a few sentences.",
                },
            ],
            "response_format": {"type": "text"},
            "n": 2,
            "seed": 12345,
            "frequency_penalty": 0.5,
            "max_tokens": 150,
            "presence_penalty": 0.3,
            "stop": ["\n\n", "END"],
            "temperature": 0.7,
            "top_k": 50,
            "top_p": 0.9,
        }

    def test_handle_request_body(self, setup_in_memory_tracer, request_body_content):
        """Test that request body handling creates proper spans with attributes"""
        memory_exporter, test_tracer = setup_in_memory_tracer

        # Check OtelTracer configuration
        current_tracer = OtelTracer.get()
        assert current_tracer is test_tracer, (
            "Current tracer does not match test tracer"
        )

        req_body = external_processor_pb2.HttpBody(
            body=json.dumps(request_body_content).encode("utf-8"),
            end_of_stream=True,
        )

        metadict = {"x-llm-provider": "openai"}
        handler = stream_handler(metadict)
        handler.req_webhook = None
        handler.req.path = "/chat/completions"

        # Create a test span in the parent span
        with test_tracer.start_as_current_span("test_parent_span"):
            response = asyncio.run(
                extproc_server.handle_request_body(
                    req_body,
                    metadict,
                    handler,
                    None,  # No parent span for this test
                )
            )

        # Verify response
        assert response is not None
        assert isinstance(response, external_processor_pb2.ProcessingResponse)

        # Verify instrumentation
        spans = memory_exporter.get_finished_spans()

        assert len(spans) >= 1, "Expected at least one span to be created"

        # Find the main request span
        gen_ai_client_span = next(
            (s for s in spans if s.name.startswith("gen_ai.request")), None
        )
        assert gen_ai_client_span is not None, (
            "Expected a gen_ai.request span to be created"
        )

        # If gen_ai_client_span found, continue verification
        attributes = gen_ai_client_span.attributes
        assert (
            attributes.get(gen_ai_attributes.GEN_AI_OPERATION_NAME) == "chat"
        )
        assert attributes.get(gen_ai_attributes.GEN_AI_SYSTEM) == handler.llm_provider
        assert (
            attributes.get(gen_ai_attributes.GEN_AI_OUTPUT_TYPE)
            == request_body_content["response_format"]["type"]
        )
        assert (
            attributes.get(gen_ai_attributes.GEN_AI_REQUEST_CHOICE_COUNT)
            == request_body_content["n"]
        )
        assert (
            attributes.get(gen_ai_attributes.GEN_AI_REQUEST_MODEL)
            == handler.request_model
        )
        assert (
            attributes.get(gen_ai_attributes.GEN_AI_REQUEST_SEED)
            == request_body_content["seed"]
        )
        assert (
            attributes.get(gen_ai_attributes.GEN_AI_REQUEST_FREQUENCY_PENALTY)
            == request_body_content["frequency_penalty"]
        )
        assert (
            attributes.get(gen_ai_attributes.GEN_AI_REQUEST_MAX_TOKENS)
            == request_body_content["max_tokens"]
        )
        assert (
            attributes.get(gen_ai_attributes.GEN_AI_REQUEST_PRESENCE_PENALTY)
            == request_body_content["presence_penalty"]
        )
        assert attributes.get(gen_ai_attributes.GEN_AI_REQUEST_STOP_SEQUENCES) == tuple(
            request_body_content["stop"]
        )
        assert (
            attributes.get(gen_ai_attributes.GEN_AI_REQUEST_TEMPERATURE)
            == request_body_content["temperature"]
        )
        assert (
            attributes.get(gen_ai_attributes.GEN_AI_REQUEST_TOP_K)
            == request_body_content["top_k"]
        )
        assert (
            attributes.get(gen_ai_attributes.GEN_AI_REQUEST_TOP_P)
            == request_body_content["top_p"]
        )

    @pytest.mark.parametrize(
        argnames="webhook_response, expected_result",
        argvalues=[
            # Pass scenario: webhook allows content through
            ({"action": {"type": "pass"}}, "passed"),
            # Modify scenario: webhook modifies content
            (
                {
                    "action": {
                        "type": "modify",
                        "body": {"messages": [{"role": "user", "content": "modified"}]},
                    }
                },
                "modified",
            ),
            # Reject scenario: webhook rejects content
            (
                {
                    "action": {
                        "type": "reject",
                        "reason": "test",
                        "status_code": 400,
                        "body": "rejected",
                    }
                },
                "rejected",
            ),
        ],
    )
    def test_handle_request_body_webhook(
        self,
        httpx_mock,
        setup_in_memory_tracer,
        request_body_content,
        webhook_response,
        expected_result,
    ):
        """Test webhook instrumentation with mocked HTTP client for different scenarios."""
        memory_exporter, test_tracer = setup_in_memory_tracer
        current_tracer = OtelTracer.get()
        assert current_tracer is test_tracer, (
            "Current tracer does not match test tracer"
        )

        # Setup HTTP mock for webhook endpoint
        httpx_mock.add_response(
            url="http://example.com:443/request",
            method="POST",
            json=webhook_response,
            status_code=200,
        )

        req_body = external_processor_pb2.HttpBody(
            body=json.dumps(request_body_content).encode("utf-8"),
            end_of_stream=True,
        )

        metadict = {"x-llm-provider": "openai"}
        handler = stream_handler(metadict)

        handler.req_webhook = prompt_guard.Webhook.from_json(
            {
                "endpoint": {"host": "example.com", "port": 443},
                "forwardHeaders": [
                    {
                        "type": "Exact",
                        "name": "Authorization",
                        "value": "Bearer test-token",
                    }
                ],
            }
        )

        # Process request with webhook
        with test_tracer.start_as_current_span("test_parent_span"):
            response = asyncio.run(
                extproc_server.handle_request_body(
                    req_body,
                    metadict,
                    handler,
                    None,
                )
            )
        assert response is not None
        assert isinstance(response, external_processor_pb2.ProcessingResponse)

        spans = memory_exporter.get_finished_spans()
        assert len(spans) >= 1, "Expected at least one span to be created"

        webhook_span = next(
            (s for s in spans if s.name.startswith("handle_request_body_req_webhook")), None
        )
        assert webhook_span is not None, (
            "Expected a handle_request_body_req_webhook span to be created"
        )

        webhook_attributes = webhook_span.attributes
        assert webhook_attributes is not None, (
            "Webhook span attributes should not be None"
        )
        assert (
            webhook_attributes.get(ai_attributes.AI_WEBHOOK_ENDPOINT)
            == str(handler.req_webhook.endpoint)
        )
        assert webhook_attributes.get(ai_attributes.AI_WEBHOOK_RESULT) == expected_result

    @pytest.mark.parametrize(
        argnames="regex_config,test_content,expected_result",
        argvalues=[
            # Phone number masking test
            (
                {
                    "matches": [{"pattern": r"\d{3}-\d{3}-\d{4}", "name": "phone"}],
                    "builtins": ["PHONE_NUMBER"],
                    "action": "MASK",
                },
                "My phone number is 123-456-7890",
                "passed",
            ),
            # Credit card rejection test
            (
                {
                    "matches": [
                        {"pattern": r"\d{4}-\d{4}-\d{4}-\d{4}", "name": "creditcard"}
                    ],
                    "builtins": ["CREDIT_CARD"],
                    "action": "REJECT",
                },
                "My credit card number is 4532-1234-5678-9012",
                "passed",  # TODO: Change to "rejected" when rejection logic is implemented
            ),
        ],
    )
    def test_handle_request_body_regex(
        self,
        setup_in_memory_tracer,
        request_body_content,
        regex_config,
        test_content,
        expected_result,
    ):
        """Test regex filtering instrumentation with different patterns and actions."""
        memory_exporter, test_tracer = setup_in_memory_tracer
        current_tracer = OtelTracer.get()
        assert current_tracer is test_tracer, (
            "Current tracer does not match test tracer"
        )

        req_body = external_processor_pb2.HttpBody(
            body=json.dumps(request_body_content).encode("utf-8"),
            end_of_stream=True,
        )

        metadict = {"x-llm-provider": "openai"}
        handler = stream_handler(metadict)

        # Configure regex filtering
        regex_config = prompt_guard.Regex.from_json(regex_config)
        handler.req_regex = init_presidio_config(regex_config)
        handler.req_regex_action = regex_config.action

        # Process request with regex filtering
        with test_tracer.start_as_current_span("test_parent_span"):
            response = asyncio.run(
                extproc_server.handle_request_body(
                    req_body,
                    metadict,
                    handler,
                    None,
                )
            )

        assert response is not None
        assert isinstance(response, external_processor_pb2.ProcessingResponse)

        spans = memory_exporter.get_finished_spans()
        assert len(spans) >= 1, "Expected at least one span to be created"

        regex_span = next(
            (s for s in spans if s.name.startswith("handle_request_body_req_regex")), None
        )
        assert regex_span is not None, (
            "Expected a handle_request_body_req_regex span to be created"
        )
        assert regex_span.attributes.get(ai_attributes.AI_REGEX_ACTION) == regex_config.action.value

        assert regex_span.attributes.get(ai_attributes.AI_REGEX_RESULT) == expected_result

    @pytest.mark.parametrize(
        argnames="moderation_flagged,expected_result",
        argvalues=[
            (False, "passed"),  # Content passes moderation
            (True, "rejected"),  # Content is rejected by moderation
        ],
    )
    def test_handle_request_body_moderation(
        self,
        setup_in_memory_tracer,
        request_body_content,
        moderation_flagged,
        expected_result,
    ):
        """Test content moderation instrumentation with different flagging scenarios."""
        memory_exporter, test_tracer = setup_in_memory_tracer
        current_tracer = OtelTracer.get()
        assert current_tracer is test_tracer
        model = "text-moderation-latest"

        # Create parameterized mock moderation client
        class ParameterizedMockModerationClient(AsyncModerations):
            async def create(self, **params):
                response = ModerationCreateResponse(
                    id="test-id",
                    model=model,
                    results=[
                        Moderation(
                            flagged=moderation_flagged,
                            categories=Categories.construct(),
                            category_scores=CategoryScores.construct(),
                            category_applied_input_types=CategoryAppliedInputTypes.construct(),
                        )
                    ],
                )
                return response

        req_body = external_processor_pb2.HttpBody(
            body=json.dumps(request_body_content).encode("utf-8"),
            end_of_stream=True,
        )

        metadict = {"x-llm-provider": "openai"}
        handler = stream_handler(metadict)

        handler.req_webhook = None
        handler.req_regex = None

        # Configure moderation
        handler.req_moderation = (
            ParameterizedMockModerationClient(client=MockAsyncOpenAI(client=None)),
            model,
        )

        with test_tracer.start_as_current_span("test_parent_span"):
            response = asyncio.run(
                extproc_server.handle_request_body(
                    req_body,
                    metadict,
                    handler,
                    None,
                )
            )

        assert response is not None
        assert isinstance(response, external_processor_pb2.ProcessingResponse)

        # Verify response behavior based on moderation result
        if expected_result == "rejected":
            # Rejected content should have immediate response
            assert hasattr(response, "immediate_response")
            assert response.immediate_response is not None
            assert (
                response.immediate_response.body == b"Rejected by guardrails moderation"
            )
        else:
            # Passed content should have normal request_body response
            assert hasattr(response, "request_body")
            assert response.request_body is not None

        spans = memory_exporter.get_finished_spans()
        assert len(spans) >= 1, "Expected at least one span to be created"

        moderation_span = next(
            (s for s in spans if s.name.startswith("handle_request_body_req_moderation")), None
        )
        assert moderation_span is not None, (
            "Expected a handle_request_body_req_moderation span to be created"
        )

        moderation_attributes = moderation_span.attributes
        assert moderation_attributes.get(ai_attributes.AI_MODERATION_MODEL) == model
        assert moderation_attributes.get(ai_attributes.AI_MODERATION_FLAGGED) == moderation_flagged

    @pytest.fixture(scope="package")
    def response_body_content(self):
        """Fixture for common response body content."""
        return """{
              "id": "fake",
              "object": "chat.completion",
              "created": 1722966273,
              "model": "gpt-4o-mini-2024-07-18",
              "choices": [
                  {
                      "index": 0,
                      "message": {
                          "role": "assistant",
                          "content": "Say hello to the world!",
                          "refusal": null
                      },
                      "logprobs": null,
                      "finish_reason": "stop"
                  }
              ],
              "usage": {
                  "prompt_tokens": 11,
                  "completion_tokens": 310,
                  "total_tokens": 321
              },
              "system_fingerprint": "fp_48196bc67a"
          }"""

    def test_handle_response_body_basic(
        self, setup_in_memory_tracer, response_body_content
    ):
        """Test basic response body handling with instrumentation."""
        memory_exporter, test_tracer = setup_in_memory_tracer
        current_tracer = OtelTracer.get()
        assert current_tracer is test_tracer

        metadict = {"x-llm-provider": "openai"}
        handler = stream_handler(metadict)

        handler.resp_webhook = None
        # Set operation name from user request path
        handler.req.path = "/chat/completions"

        resp_body = external_processor_pb2.HttpBody(
            body=response_body_content.encode("utf-8"), end_of_stream=True
        )

        # Process response body
        with test_tracer.start_as_current_span("test_parent_span"):
            response = asyncio.run(
                extproc_server.handle_response_body(
                    resp_body,
                    handler,
                    None,
                )
            )
        assert response is not None
        assert isinstance(response, external_processor_pb2.ProcessingResponse)

        # Verify instrumentation
        spans = memory_exporter.get_finished_spans()

        assert len(spans) >= 1, "Expected at least one span to be created"

        gen_ai_response = next(
            (s for s in spans if s.name.startswith("gen_ai.response")),
            None,
        )

        assert gen_ai_response is not None, (
            "Expected a gen_ai.response span to be created"
        )

        # Verify response span attributes
        attributes = gen_ai_response.attributes

        assert (
            attributes.get(gen_ai_attributes.GEN_AI_OPERATION_NAME) == "chat"
        )
        assert attributes.get(gen_ai_attributes.GEN_AI_SYSTEM) == handler.llm_provider
        assert attributes.get(gen_ai_attributes.GEN_AI_RESPONSE_ID) == "fake"
        assert (
            attributes.get(gen_ai_attributes.GEN_AI_RESPONSE_MODEL)
            == handler.get_response_model()
        )
        assert (
            attributes.get(gen_ai_attributes.GEN_AI_RESPONSE_FINISH_REASONS) == "stop"
        )
        assert (
            attributes.get(gen_ai_attributes.GEN_AI_USAGE_INPUT_TOKENS)
            == handler.get_tokens().prompt
        )
        assert (
            attributes.get(gen_ai_attributes.GEN_AI_USAGE_OUTPUT_TOKENS)
            == handler.get_tokens().completion
        )

    @pytest.mark.parametrize(
        argnames="webhook_response, expected_result",
        argvalues=[
            ({"action": {"type": "pass"}}, "passed"),
            (
                {
                    "action": {
                        "body": {
                            "choices": [
                                {
                                    "message": {
                                        "role": "Assistant",
                                        "content": "1 + 2 is 3",
                                    }
                                }
                            ]
                        }
                    }
                },
                "modified",
            ),
        ],
    )
    def test_handle_response_body_webhook(
        self,
        setup_in_memory_tracer,
        httpx_mock,
        response_body_content,
        webhook_response,
        expected_result,
    ):
        """Test response body webhook processing with instrumentation."""
        memory_exporter, test_tracer = setup_in_memory_tracer
        current_tracer = OtelTracer.get()
        assert current_tracer is test_tracer, (
            "Current tracer does not match test tracer"
        )

        # Mock the HTTP response for the webhook
        httpx_mock.add_response(
            url="http://example.com:443/response",
            method="POST",
            json=webhook_response,
            status_code=200,
        )

        metadict = {"x-llm-provider": "openai"}
        handler = stream_handler(metadict)

        # Setup response body
        resp_body = external_processor_pb2.HttpBody(
            body=response_body_content.encode("utf-8"), end_of_stream=True
        )

        # Configure response webhook
        handler.resp_webhook = prompt_guard.Webhook.from_json(
            {
                "endpoint": {"host": "example.com", "port": 443},
                "forwardHeaders": [
                    {
                        "type": "Exact",
                        "name": "Authorization",
                        "value": "Bearer test-token",
                    }
                ],
            }
        )

        # Process response with webhook
        with test_tracer.start_as_current_span("test_parent_span"):
            response = asyncio.run(
                extproc_server.handle_response_body(
                    resp_body,
                    handler,
                    None,
                )
            )
        assert response is not None
        assert isinstance(response, external_processor_pb2.ProcessingResponse)

        spans = memory_exporter.get_finished_spans()
        assert len(spans) >= 1, "Expected at least one span to be created"

        webhook_span = next(
            (s for s in spans if s.name.startswith("handle_response_body_resp_webhook")),
            None,
        )

        assert webhook_span is not None, (
            "Expected a handle_response_body_resp_webhook span to be created"
        )

        # Verify webhook span attributes
        attributes = webhook_span.attributes
        assert attributes.get(ai_attributes.AI_WEBHOOK_ENDPOINT) == str(handler.resp_webhook.endpoint)
        assert attributes.get(ai_attributes.AI_WEBHOOK_RESULT) == expected_result
