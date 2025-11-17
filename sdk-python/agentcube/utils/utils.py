import os

def get_env(key: str, default: str) -> str:
    """Get environment variable with fallback to default value
    
    Args:
        key: Environment variable name
        default: Value to return if variable doesn't exist
        
    Returns:
        Environment variable value or default
    """
    return os.getenv(key, default)

def read_token_from_file(file_path: str) -> str:
    """Read token from a file
    
    Args:
        file_path: Path to the token file
    
    Returns:
        Token string if file exists, else empty string
    """
    try:
        with open(file_path, 'r') as file:
            return file.read().strip()
    except FileNotFoundError:
        return ""