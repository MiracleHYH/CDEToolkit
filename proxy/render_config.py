#!/usr/bin/env python3

import argparse
import json
import os
import re
import stat
import tempfile
from pathlib import Path


def quoted(value: str) -> str:
    return json.dumps(value, ensure_ascii=False)


parser = argparse.ArgumentParser(description="Render a private mihomo config from a public template")
parser.add_argument("--template", required=True)
parser.add_argument("--output", required=True)
parser.add_argument("--input-file", required=True, help="0600 NUL-delimited input file")
args = parser.parse_args()

input_path = Path(args.input_file)
input_stat = input_path.lstat()
if stat.S_ISLNK(input_stat.st_mode) or not stat.S_ISREG(input_stat.st_mode):
    parser.error("input file must be a regular non-symlink file")
if input_stat.st_mode & 0o077:
    parser.error("input file must not be accessible by group or others")
fields = input_path.read_bytes().split(b"\0")
if fields and fields[-1] == b"":
    fields.pop()
if len(fields) != 5:
    parser.error("input file must contain five NUL-delimited fields")
try:
    subscription_url, isp_server, isp_port_text, isp_cipher, isp_password = (
        field.decode("utf-8") for field in fields
    )
except UnicodeDecodeError:
    parser.error("input fields must be UTF-8")

if subscription_url and not subscription_url.startswith("https://"):
    parser.error("subscription URL must use HTTPS")

template = Path(args.template)
output = Path(args.output)
text = template.read_text(encoding="utf-8")
replacements = {"__SUBSCRIPTION_URL__": quoted(subscription_url)}

isp_fields = (isp_server, isp_port_text, isp_cipher, isp_password)
if "__ISP_SERVER__" in text:
    if any(not value for value in isp_fields):
        parser.error("ISP template requires server, port, cipher, and password")
    try:
        isp_port = int(isp_port_text)
    except ValueError:
        parser.error("ISP port must be an integer")
    if not 1 <= isp_port <= 65535:
        parser.error("ISP port must be between 1 and 65535")
    if any(char.isspace() for char in isp_server):
        parser.error("ISP server is invalid")
    if any(char.isspace() for char in isp_cipher):
        parser.error("ISP cipher is invalid")
    replacements.update(
        {
            "__ISP_SERVER__": quoted(isp_server),
            "__ISP_PORT__": str(isp_port),
            "__ISP_CIPHER__": quoted(isp_cipher),
            "__ISP_PASSWORD__": quoted(isp_password),
        }
    )

for marker, value in replacements.items():
    text = text.replace(marker, value)
if re.search(r"__[A-Z0-9_]+__", text):
    parser.error("template contains an unresolved placeholder")

output.parent.mkdir(parents=True, exist_ok=True)
fd, temporary_name = tempfile.mkstemp(prefix=f".{output.name}.", dir=output.parent)
try:
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        handle.write(text)
    os.chmod(temporary_name, 0o600)
    os.replace(temporary_name, output)
finally:
    if os.path.exists(temporary_name):
        os.unlink(temporary_name)
