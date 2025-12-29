"""HTTP session utilities for AgentCube SDK."""

import requests
from requests.adapters import HTTPAdapter
from urllib3.util.retry import Retry


def create_session(
    pool_connections: int = 10,
    pool_maxsize: int = 10,
    retry_total: int = 3,
    retry_backoff_factor: float = 0.5,
    retry_status_forcelist: tuple = (502, 503, 504),
) -> requests.Session:
    """Create a requests Session with connection pooling and retry strategy.
    
    Args:
        pool_connections: Number of connection pools to cache (default: 10).
        pool_maxsize: Maximum connections per pool (default: 10).
        retry_total: Maximum number of retries (default: 3).
        retry_backoff_factor: Backoff factor for retries (default: 0.5).
        retry_status_forcelist: HTTP status codes to retry on (default: 502, 503, 504).
    
    Returns:
        A configured requests.Session object with connection pooling and retry strategy.
    """
    session = requests.Session()
    
    retry_strategy = Retry(
        total=retry_total,
        backoff_factor=retry_backoff_factor,
        status_forcelist=retry_status_forcelist,
    )
    
    adapter = HTTPAdapter(
        pool_connections=pool_connections,
        pool_maxsize=pool_maxsize,
        max_retries=retry_strategy,
    )
    
    session.mount("http://", adapter)
    session.mount("https://", adapter)
    
    return session
