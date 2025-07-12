from client.client import LLMClient
import logging

import requests
import json

logging.basicConfig(level=logging.DEBUG)
logger = logging.getLogger(__name__)

class TestTracingNonStreamRouting(LLMClient):
    def test_openai_completion(self):
        resp = self.openai_client.chat.completions.create(
            model="gpt-4o-mini",
            messages=[
                {
                    "role": "system",
                    "content": "You are a poetic assistant, skilled in explaining complex programming concepts with creative flair.",
                },
                {
                    "role": "user",
                    "content": "Compose a poem that explains the concept of recursion in programming.",
                },
            ],
            max_tokens=150,
            temperature=0.7,
            top_p=0.9,
            n=2,
            seed=12345,
            frequency_penalty=0.5,
            presence_penalty=0.3,
            response_format={"type": "text"},
            stop=["\n\n", "END"],
        )
        logger.debug(f"openai routing response:\n{resp}")
        assert (
            resp is not None
            and len(resp.choices) > 0
            and resp.choices[0].message.content is not None
        )
        assert (
            resp.usage is not None
            and resp.usage.prompt_tokens > 0
            and resp.usage.completion_tokens > 0
        )

        requests.post()