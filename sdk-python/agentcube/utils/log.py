import logging

# Initialize logger configuration once
def get_logger(name: str) -> logging.Logger:
    """Get a logger instance with basic configuration"""
    logger = logging.getLogger(name)
    
    # Configure only if no handlers are already set
    if not logger.handlers:
        logger.setLevel(logging.INFO)
        handler = logging.StreamHandler()
        formatter = logging.Formatter(
            '%(asctime)s | %(levelname)-4s | %(name)-20s | %(message)s'
        )
        handler.setFormatter(formatter)
        logger.addHandler(handler)
    
    return logger