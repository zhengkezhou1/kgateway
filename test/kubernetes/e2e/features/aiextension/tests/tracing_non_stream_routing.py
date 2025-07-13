import os
from client.client import LLMClient
import logging
import tempfile
import time

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

        # 你的 Tempo 查询服务地址
        TEMPO_QUERY_URL = os.environ.get("TEST_TEMPO_URL", "")

        traceql_query = '{ name="/openai gpt-4o-mini" }'

        params = {
            'q': traceql_query
        }
    
        time.sleep(120)
        # Send GET request
        resp = requests.get(f"{TEMPO_QUERY_URL}/api/search", params=params)
        logger.info(f"tempo query response:\n{resp.text}")
        
        # 将响应写入临时文件
        with tempfile.NamedTemporaryFile(mode='w', suffix='.json', prefix='tempo_response_', delete=False) as f:
            response_data = {
                "timestamp": time.strftime("%Y-%m-%d %H:%M:%S"),
                "tempo_url": TEMPO_QUERY_URL,
                "query_params": params,
                "status_code": resp.status_code,
                "headers": dict(resp.headers),
                "response_text": resp.text,
                "response_json": None
            }
            
            # 尝试解析 JSON 响应
            try:
                response_data["response_json"] = resp.json()
            except Exception as e:
                response_data["json_parse_error"] = str(e)
            
            json.dump(response_data, f, indent=2, ensure_ascii=False)
            temp_file_path = f.name
    