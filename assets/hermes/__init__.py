"""membraid for Hermes: put the shared-memory digest in front of the model on
the first turn of each session.

pre_llm_call fires on every turn. Only the first turn gets the digest, so it is
paid for once and cannot shift mid-conversation as the agent writes to memory.
Hermes appends returned context to the user message rather than the system
prompt, which keeps the prompt cache intact.

Any failure yields no context - a missing binary or an unusable vault must
never break a turn.
"""

import os
import subprocess


def _binary():
    return os.environ.get("MEMBRAID_BIN") or os.path.expanduser("~/go/bin/membraid")


def _digest(cwd=None):
    try:
        result = subprocess.run(
            [_binary(), "context"],
            cwd=cwd or os.getcwd(),
            capture_output=True,
            text=True,
            timeout=10,
        )
    except Exception:
        return ""
    return result.stdout.strip() if result.returncode == 0 else ""


def pre_llm_call(session_id=None, user_message=None, conversation_history=None,
                 is_first_turn=False, model=None, platform=None, **kwargs):
    if not is_first_turn:
        return None
    return _digest() or None


def register(ctx):
    ctx.register_hook("pre_llm_call", pre_llm_call)
