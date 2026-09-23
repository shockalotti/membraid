"""membraid for Hermes: put the shared-memory digest in front of the model on
the first turn of each session, and a one-line write scan on close-out turns.

pre_llm_call fires on every turn. Only the first turn gets the digest, so it is
paid for once and cannot shift mid-conversation as the agent writes to memory.
Hermes appends returned context to the user message rather than the system
prompt, which keeps the prompt cache intact.

The scan line fires at most once per session, when the user's message looks
like a close-out ("ok perfect", "defer that", ...). Skill paragraphs rot under
task context; this line arrives at the moment it applies, from outside the
agent's discretion - which is the whole point, since agents quote the skill
rules while violating all of them.

Any failure yields no context - a missing binary or an unusable vault must
never break a turn.
"""

import json
import os
import re
import subprocess
from datetime import datetime, timezone


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


SCAN_LINE = (
    "Memory scan before you finish: a decision, discovery, or correction "
    "this turn gets a memory_write; a memory that guided you gets a memory_used."
)

# Close-out phrases from real sessions where writes never happened. Kept
# tight: a false positive costs one line, a false negative costs the memory.
_CLOSEOUT = re.compile(
    r"(?i)\b(we are good|not now|defer|deferred|agreed|awesome|perfect|"
    r"excellent|thanks|thank you|looks good|lgtm|ship it|go ahead|"
    r"ok(ay)?( it works| perfect| great| good| thanks)?[.!]*$)"
)

# Sessions already nudged, so the scan fires at most once per session.
_nudged = set()


def _log_nudge(session_id):
    # Telemetry for the write-compliance question: injections correlated with
    # later writes by session and time. Never breaks the turn.
    try:
        path = os.path.expanduser("~/.membraid/nudge.jsonl")
        with open(path, "a") as f:
            f.write(json.dumps({
                "ts": datetime.now(timezone.utc).isoformat(),
                "channel": "hermes-nudge",
                "session": str(session_id),
            }) + "\n")
    except Exception:
        pass


def _is_closeout(user_message, conversation_history):
    texts = []
    if isinstance(user_message, str) and user_message.strip():
        texts.append(user_message)
    try:
        if conversation_history:
            last = conversation_history[-1]
            texts.append(last if isinstance(last, str) else str(last))
    except Exception:
        pass
    # A close-out is short ("ok perfect") or ends with the phrase. A bare
    # "thanks" early in a long message with a follow-up question ("thanks,
    # but why...") is not a close-out. Either way the nudge fires at most
    # once per session, so a false positive costs one line while a false
    # negative costs the memory.
    for t in texts:
        s = t.strip()
        if not s:
            continue
        m = _CLOSEOUT.search(s)
        if m and (len(s) <= 40 or m.end() >= len(s) - 12):
            return True
    return False


def pre_llm_call(session_id=None, user_message=None, conversation_history=None,
                 is_first_turn=False, model=None, platform=None, **kwargs):
    if is_first_turn:
        return _digest() or None
    try:
        if session_id in _nudged:
            return None
        if _is_closeout(user_message, conversation_history):
            _nudged.add(session_id)
            _log_nudge(session_id)
            return SCAN_LINE
    except Exception:
        pass
    return None


def register(ctx):
    ctx.register_hook("pre_llm_call", pre_llm_call)
