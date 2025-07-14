import asyncio
import json
import logging
from wsgiref import headers
import pytest

from telemetry.stats import Config as StatsConfig
from telemetry.tracing import Config as TracingConfig
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

# Additional imports for instrumentation testing
from unittest.mock import patch, MagicMock, AsyncMock, call
from unittest import mock
from opentelemetry.sdk.trace import TracerProvider, Span
from opentelemetry.semconv._incubating.attributes import gen_ai_attributes


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


class TestExtProcServerInstrumentation:
    """Test class specifically for ExtProcServer instrumentation functionality"""

    def test_handle_request_body_non_stream_span(self):
        """Test OpenTelemetry span creation and attribute setting when processing non stream request body"""
        async def run_test():
            request_body = {
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
                "response_format": {
                    "type": "text"
                },
                "n": 2,
                "seed": 12345,
                "frequency_penalty": 0.5,
                "max_tokens": 150,
                "presence_penalty": 0.3,
                "stop": ["\n\n", "END"],
                "temperature": 0.7,
                "top_k": 50,
                "top_p": 0.9,
                "stream": False
            }
            
            req_body = external_processor_pb2.HttpBody(
                body=json.dumps(request_body).encode('utf-8'),
                end_of_stream=True,
            )
            
            headers = external_processor_pb2.HttpHeaders()
            headers.headers.headers.add(key="path", value="/chat/completions")
            
            metadict = {"x-llm-provider": "openai"}
            handler = StreamHandler.from_metadata(metadict)
            
            handler.req.set_headers(headers=headers, header_rules=[])
            handler.req_webhook = None
            
            # Use mock span to verify span creation logic
            with patch('telemetry.tracing.OtelTracer.get') as mock_tracer_get:
                mock_tracer = MagicMock()
                mock_span = MagicMock()
                mock_tracer.start_as_current_span.return_value.__enter__.return_value = mock_span
                mock_tracer_get.return_value = mock_tracer
                
                parent_span = trace.NonRecordingSpan(trace.SpanContext(0, 0, False))
                response = await extproc_server.handle_request_body(
                    req_body, metadict, handler, parent_span
                )

                # Verify response
                assert response is not None
                assert isinstance(response, external_processor_pb2.ProcessingResponse)

                # Verify span was created
                mock_tracer.start_as_current_span.assert_called()
                span_call_args = mock_tracer.start_as_current_span.call_args
                assert f"gen_ai.request {handler.req.path} {handler.request_model}" == span_call_args[0][0]

                # Verify span attribute setting - corresponding to gen_ai_attributes in server.py
                span_kwargs = span_call_args[1]  # Get keyword arguments
                assert 'attributes' in span_kwargs, "Span should be created with attributes"
                
                attributes = span_kwargs['attributes']
                
                # Verify all expected gen_ai attributes and their values
                expected_attributes = {
                    gen_ai_attributes.GEN_AI_OPERATION_NAME: handler.req.path,  
                    gen_ai_attributes.GEN_AI_SYSTEM: handler.llm_provider,                    
                    gen_ai_attributes.GEN_AI_OUTPUT_TYPE: request_body["response_format"]["type"],                 
                    gen_ai_attributes.GEN_AI_REQUEST_CHOICE_COUNT: request_body["n"],             
                    gen_ai_attributes.GEN_AI_REQUEST_MODEL: handler.request_model,        
                    gen_ai_attributes.GEN_AI_REQUEST_SEED: request_body["seed"],                 
                    gen_ai_attributes.GEN_AI_REQUEST_FREQUENCY_PENALTY: request_body["frequency_penalty"],      
                    gen_ai_attributes.GEN_AI_REQUEST_MAX_TOKENS: request_body["max_tokens"],             
                    gen_ai_attributes.GEN_AI_REQUEST_PRESENCE_PENALTY: request_body["presence_penalty"],       
                    gen_ai_attributes.GEN_AI_REQUEST_STOP_SEQUENCES: request_body["stop"],  
                    gen_ai_attributes.GEN_AI_REQUEST_TEMPERATURE: request_body["temperature"],            
                    gen_ai_attributes.GEN_AI_REQUEST_TOP_K: request_body["top_k"],              
                    gen_ai_attributes.GEN_AI_REQUEST_TOP_P: request_body["top_p"],                  
                }
                
                # Verify each attribute value
                for attr_name, expected_value in expected_attributes.items():
                    assert attr_name in attributes, f"Missing attribute: {attr_name}"
                    actual_value = attributes[attr_name]
                    assert actual_value == expected_value, \
                        f"Attribute {attr_name}: expected {expected_value}, got {actual_value}"
                
                print(f"✅ All {len(expected_attributes)} span attributes verified successfully!")
                print(f"Verified attributes: {list(expected_attributes.keys())}")
                
                # Verify span was created with correct context
                assert 'context' in span_kwargs, "Span should be created with context"
        
        asyncio.run(run_test())
        
    def test_handle_response_body_non_stream_span(self):
        """Test OpenTelemetry span creation and attribute setting when processing non stream response body"""
        async def run_test():
            response_body = {
                "id": "chatcmpl-1234567890",
                "object": "chat.completion",
                "created": 1722966273,
                "model": "gpt-4o-mini-2024-07-18",
                "choices": [
                    {
                        "index": 0,
                        "message": {
                            "role": "assistant",
                            "content": "Recursion is like a mirror reflecting itself, endlessly repeating the same pattern."
                        },
                        "logprobs": None,
                        "finish_reason": "stop"
                    }
                ],
                "usage": {
                    "prompt_tokens": 11,
                    "completion_tokens": 310,
                    "total_tokens": 321
                },
                "system_fingerprint": "fp_48196bc67a"
            }
            
            resp_body = external_processor_pb2.HttpBody(
                body=json.dumps(response_body).encode('utf-8'),
                end_of_stream=True,
            )
            
            headers = external_processor_pb2.HttpHeaders()
            headers.headers.headers.add(key="path", value="/chat/completions")
            
            metadict = {"x-llm-provider": "openai"}
            handler = StreamHandler.from_metadata(metadict)
            
            handler.req.set_headers(headers=headers, header_rules=[])
            handler.resp_regex = None
            
            # Use mock span to verify span creation logic
            with patch('telemetry.tracing.OtelTracer.get') as mock_tracer_get:
                mock_tracer = MagicMock()
                mock_span = MagicMock()
                mock_tracer.start_as_current_span.return_value.__enter__.return_value = mock_span
                mock_tracer_get.return_value = mock_tracer
                
                parent_span = trace.NonRecordingSpan(trace.SpanContext(0, 0, False))
                response = await extproc_server.handle_response_body(
                    resp_body, handler, parent_span
                )

                # Verify response
                assert response is not None
                assert isinstance(response, external_processor_pb2.ProcessingResponse)
                
                mock_tracer.start_as_current_span.assert_called()
                span_call_args = mock_tracer.start_as_current_span.call_args
                expected_span_name = f"gen_ai.non_streaming_response {handler.req.path}"
                assert expected_span_name == span_call_args[0][0]

                # Since we're using set_attributes instead of initial attributes,
                # check that set_attributes was called on the mock span
                mock_span.set_attributes.assert_called_once()
                # Verify all expected gen_ai attributes and their values

                # Get the attributes that were set
                attributes_call_args = mock_span.set_attributes.call_args
                attributes = attributes_call_args[0][0]  # First positional argument

                expected_attributes = {
                    gen_ai_attributes.GEN_AI_OPERATION_NAME: handler.req.path,
                    gen_ai_attributes.GEN_AI_SYSTEM: handler.llm_provider,
                    gen_ai_attributes.GEN_AI_RESPONSE_ID: response_body["id"],
                    gen_ai_attributes.GEN_AI_RESPONSE_MODEL: handler.get_response_model(),
                    gen_ai_attributes.GEN_AI_RESPONSE_FINISH_REASONS: response_body["choices"][0]["finish_reason"],
                    gen_ai_attributes.GEN_AI_USAGE_INPUT_TOKENS: handler.get_tokens().prompt,
                    gen_ai_attributes.GEN_AI_USAGE_OUTPUT_TOKENS: handler.get_tokens().completion,
                }
                
                for attr_name, expected_value in expected_attributes.items():
                    assert attr_name in attributes, f"Missing attribute: {attr_name}"
                    actual_value = attributes[attr_name]
                    assert actual_value == expected_value, f"Attribute {attr_name}: expected {expected_value}, got {actual_value}"
                
                print(f"✅ All {len(expected_attributes)} span attributes verified successfully!")
                print(f"Verified attributes: {list(expected_attributes.keys())}")
                
                # Verify span was created with correct context
                span_kwargs = span_call_args[1]  # Get keyword arguments from span creation
                assert 'context' in span_kwargs, "Span should be created with context"
                
        asyncio.run(run_test())