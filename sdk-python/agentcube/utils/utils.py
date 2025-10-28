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