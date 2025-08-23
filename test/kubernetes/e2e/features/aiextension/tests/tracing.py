import logging
import pytest
import os
logger = logging.getLogger(__name__)
logger.setLevel(logging.DEBUG)
from client.client import LLMClient
from google.generativeai.types import content_types
from google.generativeai.types import generation_types
from openai.types.chat import completion_create_params
class TestTracing(LLMClient):
    def test_openai_completion(self):
        resp = self.openai_client.chat.completions.create(
            model="gpt-4o-mini",
            messages=[
                {
                    "role": "system",
                    "content": "You are a poetic assistant, skilled in explaining complex programming concepts with creative flair."
			    },
			    {
                    "role": "user",
                    "content": "Compose a poem that explains the concept of recursion in programming."
			    },
            ],
            response_format={"type": "text"},
            n=2,
            seed=12345,
            frequency_penalty=0.5,
            max_tokens=150,
            presence_penalty=0.3,
            stop=["\n\n", "END"],
            temperature=0.7,
            top_p=0.9,
        )
        logger.debug(f"openai routing response:\n{resp}")
        assert (
            resp is not None
            and len(resp.choices) > 0
            and resp.choices[0].message.content is not None
        )
    
    @pytest.mark.skipif(
        os.environ.get("TEST_TOKEN_PASSTHROUGH") == "true",
        reason="passthrough not enabled for gemini",
    )
    @pytest.mark.skipif(
        os.environ.get("TEST_OVERRIDE_PROVIDER") == "true",
        reason="overrideProvider not enabled for gemini",
    )
    def test_gemini(self):
        resp = self.gemini_client.generate_content(
            contents="Write a short story about a detective and a mysterious case.",
            generation_config=generation_types.GenerationConfig(
                stop_sequences=["THE END","end of story."],
                candidate_count=1,
                max_output_tokens=5000,
                temperature=0.9,
                top_p=0.95,
                top_k=40,
                frequency_penalty=0.5,
                presence_penalty=0.3
            )
        )
        assert resp is not None
        logger.debug(f"gemini routing response:\n{resp}")