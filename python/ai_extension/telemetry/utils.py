import base64
import binascii
import json
import logging

logger = logging.getLogger(__name__)

def read_mounted_config_map(file_path: str) -> dict:
    """
    Read configuration from a Kubernetes ConfigMap mounted file.
    
    This function handles both:
    1. Direct JSON content (most common in ConfigMaps)
    2. Base64 encoded JSON content (less common)
    
    Args:
        file_path: Path to the mounted ConfigMap file
        
    Returns:
        dict: Parsed configuration data
        
    Raises:
        FileNotFoundError: If the file doesn't exist
        json.JSONDecodeError: If the content is not valid JSON
        ValueError: If the content cannot be decoded
    """
    with open(file_path, "r", encoding="utf-8") as file:
        # Read the file content as text
        content = file.read().strip()
    
    # Try to parse as direct JSON first (most common case)
    try:
        return json.loads(content)
    except json.JSONDecodeError:
        logger.debug(f"Failed to parse as direct JSON, trying base64 decoding for {file_path}")
        
        # If direct JSON parsing fails, try base64 decoding
        try:
            # Step 1: Base64 decode the content
            decoded_bytes = base64.b64decode(content)
            
            # Step 2: Convert the decoded bytes to a UTF-8 string
            json_string = decoded_bytes.decode("utf-8")
            
            # Step 3: Parse the JSON content
            return json.loads(json_string)
        except (binascii.Error, json.JSONDecodeError, UnicodeDecodeError) as e:
            logger.error(f"Failed to decode content from {file_path}: {e}")
            raise ValueError(f"Unable to parse content from {file_path} as JSON or base64-encoded JSON") from e
