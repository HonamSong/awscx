"""python -m awscx 진입점."""
import sys

try:
    import boto3  # noqa: F401
    import pyte  # noqa: F401
    import textual  # noqa: F401
except ImportError as e:
    sys.exit(f"의존성 부족({e}):  pip install boto3 textual pyte")

from awscx.app import main

if __name__ == "__main__":
    main()
