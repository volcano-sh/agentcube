import logging
import sys
from logging.handlers import RotatingFileHandler
from pathlib import Path
from typing import Dict, Any, Optional


def _format_context(context: Dict[str, Any]) -> str:
    return " | ".join(f"{k}={v}" for k, v in context.items())

def _configure_handlers(log_file: Optional[str]):
    """Configure handlers at the root level"""
    root_logger = logging.getLogger()
    root_logger.setLevel(logging.INFO)

    formatter = logging.Formatter(
        '%(asctime)s | %(levelname)-8s | %(name)-40s | %(message)s'
    )

    # Console handler
    console_handler = logging.StreamHandler(sys.stdout)
    console_handler.setFormatter(formatter)
    root_logger.addHandler(console_handler)

    # File handler if specified
    if log_file:
        Path(log_file).parent.mkdir(parents=True, exist_ok=True)
        file_handler = RotatingFileHandler(
            log_file,
            maxBytes=10 * 1024 * 1024,  # 10MB
            backupCount=5,
            encoding='utf-8'
        )
        file_handler.setFormatter(formatter)
        root_logger.addHandler(file_handler)


class StructuredLogger:
    _global_handlers_configured = False

    def __init__(self, name: str, log_file: Optional[str] = None, level: str = "INFO"):
        """
        Create a hierarchical logger with parent-child relationships
        Example:
        - core (parent)
          - core.models (child)
        - providers (parent)
          - providers.kubernetes (child)
            - providers.kubernetes.lifecycle (grandchild)
        """
        self._logger = logging.getLogger(name)
        self._logger.setLevel(getattr(logging, level))
        self._logger.propagate = True  # Allow propagation to parent loggers

        # Only configure handlers once at the root level
        if not StructuredLogger._global_handlers_configured:
            _configure_handlers(log_file)
            StructuredLogger._global_handlers_configured = True

    def info(self, message: str, context: Optional[Dict[str, Any]] = None):
        self._log(logging.INFO, message, context)

    def error(self, message: str, context: Optional[Dict[str, Any]] = None):
        self._log(logging.ERROR, message, context)

    def debug(self, message: str, context: Optional[Dict[str, Any]] = None):
        self._log(logging.DEBUG, message, context)

    def warning(self, message: str, context: Optional[Dict[str, Any]] = None):
        self._log(logging.WARN, message, context)

    def _log(self, level: int, message: str, context: Optional[Dict[str, Any]]):
        if context is None:
            context = {}

        # Convert context to string only if present
        message = f"{message} | {_format_context(context)}"

        self._logger.log(level, message)


def get_logger(name: str) -> StructuredLogger:
    """Factory function to get a hierarchical logger"""
    return StructuredLogger(name)
