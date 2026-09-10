"""단일 버전 소스.

- __version__ : PyPI/빌드용 실제 버전 (항상 PEP440 클린 값, 예: "0.2.0")
- DEV         : 개발 중이면 True → 화면에 'dv0.2.0'.
                릴리스/빌드해서 PyPI 올릴 땐 False → 화면에 'v0.2.0'.
  (PyPI 업로드 버전은 DEV 와 무관하게 항상 __version__ 그대로 = 0.2.0)
"""
__version__ = "0.2.3"
DEV = True


def version_label():
    """화면 표시용 버전 문자열 (dv0.2.0 / v0.2.0)."""
    return ("dv" if DEV else "v") + __version__
