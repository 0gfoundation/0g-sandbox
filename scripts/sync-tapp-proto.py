#!/usr/bin/env python3
"""Regenerate the wire-compatible Go client subset from a local tapp checkout.

Usage: PATH="$HOME/go/bin:$PATH" python3 scripts/sync-tapp-proto.py ../0g-tapp/proto/tapp_service.proto
Requires protoc, protoc-gen-go, and protoc-gen-go-grpc on PATH.
"""
import pathlib
import re
import subprocess
import sys

root = pathlib.Path(__file__).resolve().parents[1]
source = pathlib.Path(sys.argv[1]).read_text()
methods = ["GetAppSecretKey", "GetSecretResource", "GetAppSigner", "GetAppTlsCert", "ResolveAppIdentity"]
messages = [name + suffix for name in methods for suffix in ("Request", "Response")]
messages += ["ResolvedAppNodeIdentity", "ResolvedKeyIdentity", "ResolvedKeyCommitment"]

def definition(name):
    match = re.search(r"^message " + re.escape(name) + r"\s*\{", source, re.MULTILINE)
    if match is None:
        raise ValueError("missing tapp message: " + name)
    depth = 1
    end = match.end()
    while depth:
        if source[end] == "{":
            depth += 1
        elif source[end] == "}":
            depth -= 1
        end += 1
    return source[match.start():end]

header = '''syntax = "proto3";
package tapp_service;
option go_package = "github.com/0gfoundation/0g-sandbox/internal/tee/tapp_service";

// Generated wire-compatible subset; see scripts/sync-tapp-proto.py.
service TappService {
'''
service = "".join(f"  rpc {name}({name}Request) returns ({name}Response);\n" for name in methods)
target = root / "internal/tee/tapp_service"
(target / "tapp_service.proto").write_text(header + service + "}\n\n" + "\n\n".join(map(definition, messages)) + "\n")
subprocess.run([
    "protoc", "--go_out=.", "--go_opt=paths=source_relative",
    "--go-grpc_out=.", "--go-grpc_opt=paths=source_relative", "tapp_service.proto",
], cwd=target, check=True)
