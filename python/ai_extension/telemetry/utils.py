import json


def read_mounted_config_map(file_path: str) -> dict:
    """
    Read and parse JSON configuration from a mounted ConfigMap file.

    """
    with open(file_path, "r", encoding="utf-8") as file:
        content = file.read().strip()

    return json.loads(content)
