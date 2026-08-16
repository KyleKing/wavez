import json
import urllib.request

TOOLS = [{
    "type": "function",
    "function": {
        "name": "rename_symbol",
        "description": "Rename a symbol (function, variable, or type) across a Go file.",
        "parameters": {
            "type": "object",
            "properties": {
                "file": {"type": "string", "description": "path to the Go file"},
                "old_name": {"type": "string"},
                "new_name": {"type": "string"},
            },
            "required": ["file", "old_name", "new_name"],
        },
    },
}]

PROMPT = ("In internal/store/store.go, rename the function `PostTransaction` to `RecordTransaction`. "
          "Call the rename_symbol tool to do it. Do not explain, just call the tool.")

def call():
    body = {
        "model": "qwen3-8b",
        "messages": [{"role": "user", "content": PROMPT}],
        "tools": TOOLS,
        "reasoning_effort": "none",
    }
    req = urllib.request.Request("http://127.0.0.1:8090/v1/chat/completions", data=json.dumps(body).encode(),
                                  headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req) as resp:
        return json.loads(resp.read())

if __name__ == "__main__":
    results = []
    for i in range(3):
        obj = call()
        msg = obj["choices"][0]["message"]
        tc = msg.get("tool_calls")
        well_formed = False
        parsed = None
        if tc and len(tc) == 1 and tc[0]["function"]["name"] == "rename_symbol":
            try:
                args = json.loads(tc[0]["function"]["arguments"])
                well_formed = set(args.keys()) == {"file", "old_name", "new_name"} and \
                    args["old_name"] == "PostTransaction" and args["new_name"] == "RecordTransaction"
                parsed = args
            except json.JSONDecodeError:
                well_formed = False
        results.append({"try": i + 1, "well_formed": well_formed, "tool_calls": tc, "parsed_args": parsed, "content": msg.get("content")})
    print(json.dumps(results, indent=2))
