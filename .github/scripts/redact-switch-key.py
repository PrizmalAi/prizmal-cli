#!/usr/bin/env python3
"""Filter stdin, replacing every form of the Switch key with ***.

GitHub Actions already masks the verbatim value in log output. The residual
risk this covers is a harness echoing the key *transformed*: opencode receives
it inside the OPENCODE_CONFIG_CONTENT JSON blob, and a config parse error
there can print that content back, potentially escaped or re-encoded. So the
harness diagnostics printed by the launch steps go through here first.

Reads PRIZMAL_SWITCH_KEY from the environment; a pass-through no-op when it is
unset. Never prints the key, its length, or any digest of it.
"""

import base64
import json
import os
import sys
import urllib.parse


def key_forms(key: str) -> list[str]:
    if not key:
        return []
    raw = key.encode()
    forms = {
        key,
        base64.b64encode(raw).decode(),
        base64.urlsafe_b64encode(raw).decode().rstrip("="),
        urllib.parse.quote(key, safe=""),
        raw.hex(),
        json.dumps(key)[1:-1],
    }
    # Longest first, so a form that contains a shorter one still redacts whole.
    return sorted((f for f in forms if f), key=len, reverse=True)


def main() -> int:
    forms = key_forms(os.environ.get("PRIZMAL_SWITCH_KEY", ""))
    for line in sys.stdin:
        for form in forms:
            line = line.replace(form, "***")
        sys.stdout.write(line)
    return 0


if __name__ == "__main__":
    sys.exit(main())
