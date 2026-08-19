#!/usr/bin/env python3
"""Count the stable prefix in the served model's own tokens.

usage: measure.py prefix.json [http://127.0.0.1:8080]

prefix.json holds {"system": <SystemPrefix>, "tools": [<OpenAI tool objects>]}
as wavez assembles them for a project (see README.md for how to dump it).
The server must be llama-server started with --jinja, so /apply-template
renders the same chat template a request goes through.
"""
import json
import sys
import urllib.request

d = json.load(open(sys.argv[1]))
base = sys.argv[2] if len(sys.argv) > 2 else "http://127.0.0.1:8080"


def post(path, body):
    req = urllib.request.Request(base + path, data=json.dumps(body).encode(), headers={"Content-Type": "application/json"})
    return json.load(urllib.request.urlopen(req))


def ntok(text):
    return len(post("/tokenize", {"content": text})["tokens"])


def rendered(msgs, tools=None):
    body = {"messages": msgs}
    if tools:
        body["tools"] = tools
    return post("/apply-template", body)["prompt"]


user = [{"role": "user", "content": "x"}]
sysm = [{"role": "system", "content": d["system"]}] + user
p_user, p_sys, p_all = ntok(rendered(user)), ntok(rendered(sysm)), ntok(rendered(sysm, d["tools"]))
chars = len(d["system"]) + len(json.dumps(d["tools"]))
print(f"system text alone: {ntok(d['system'])} tokens")
print(f"tools json alone: {ntok(json.dumps(d['tools']))} tokens")
print(f"through the template: user only {p_user}, system+user {p_sys}, system+tools+user {p_all}")
print(f"stable prefix about {p_all - p_user} tokens; chars/4 estimate {chars // 4}")
