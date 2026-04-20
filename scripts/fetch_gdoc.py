#!/usr/bin/env python3
"""Fetch a Google Doc and output it as Markdown.

Usage:
  fetch_gdoc.py auth <credentials.json> [<token.json>]
      Run the OAuth consent flow and save the token. Opens a browser window.
      Only needs to be done once.

  fetch_gdoc.py fetch <doc_id_or_url> <credentials.json> [<token.json>]
      Fetch the document and write Markdown to stdout.

The default token path is ~/.config/nexus/gdoc_token.json.
"""
import os
import re
import sys
from pathlib import Path

DEFAULT_TOKEN = Path.home() / ".config" / "nexus" / "gdoc_token.json"
SCOPES = ["https://www.googleapis.com/auth/documents.readonly"]


def _get_creds(creds_path: str, token_path: str):
    from google.auth.transport.requests import Request
    from google.oauth2.credentials import Credentials
    from google_auth_oauthlib.flow import InstalledAppFlow

    creds = None
    if os.path.exists(token_path):
        creds = Credentials.from_authorized_user_file(token_path, SCOPES)
    if not creds or not creds.valid:
        if creds and creds.expired and creds.refresh_token:
            creds.refresh(Request())
        else:
            flow = InstalledAppFlow.from_client_secrets_file(creds_path, SCOPES)
            creds = flow.run_local_server(port=0)
        Path(token_path).parent.mkdir(parents=True, exist_ok=True)
        Path(token_path).write_text(creds.to_json())
    return creds


def _extract_doc_id(url_or_id: str) -> str:
    m = re.search(r"/document/d/([a-zA-Z0-9_-]+)", url_or_id)
    return m.group(1) if m else url_or_id


def _element_text(e: dict) -> str:
    """Extract plain text from any ParagraphElement type."""
    if "textRun" in e:
        return e["textRun"].get("content", "")
    if "dateElement" in e:
        return e["dateElement"].get("dateElementProperties", {}).get("displayText", "")
    if "person" in e:
        return e["person"].get("personProperties", {}).get("name", "")
    if "richLinkElement" in e:
        return e["richLinkElement"].get("richLinkProperties", {}).get("title", "")
    return ""


def _doc_to_markdown(doc: dict) -> str:
    """Convert a Google Docs JSON document to Markdown text."""
    title = doc.get("title", "Untitled")
    lines = [f"# {title}", ""]

    lists = doc.get("lists", {})
    content = doc.get("body", {}).get("content", [])

    for elem in content:
        para = elem.get("paragraph")
        if not para:
            table = elem.get("table")
            if table:
                for row in table.get("tableRows", []):
                    cells = []
                    for cell in row.get("tableCells", []):
                        cell_text = ""
                        for ce in cell.get("content", []):
                            cp = ce.get("paragraph")
                            if cp:
                                cell_text += "".join(
                                    e.get("textRun", {}).get("content", "")
                                    for e in cp.get("elements", [])
                                ).strip()
                        cells.append(cell_text)
                    lines.append("| " + " | ".join(cells) + " |")
                lines.append("")
            continue

        style = para.get("paragraphStyle", {}).get("namedStyleType", "NORMAL_TEXT")
        elements = para.get("elements", [])
        text = "".join(_element_text(e) for e in elements).rstrip("\n")

        heading_map = {
            "HEADING_1": "##",
            "HEADING_2": "###",
            "HEADING_3": "####",
            "HEADING_4": "#####",
        }

        # Headings take priority — a list item styled as Heading 1 is a section
        # divider (e.g. a date in a 1:1 doc), not a list item.
        if style in heading_map:
            if text.strip():
                lines.append(f"{heading_map[style]} {text.strip()}")
            continue

        # List items (non-heading only)
        bullet = para.get("bullet")
        if bullet:
            nesting = bullet.get("nestingLevel", 0)
            list_id = bullet.get("listId", "")
            nesting_levels = (
                lists.get(list_id, {})
                .get("listProperties", {})
                .get("nestingLevels", [])
            )
            is_ordered = False
            if nesting < len(nesting_levels):
                glyph = nesting_levels[nesting].get("glyphType", "")
                is_ordered = glyph in ("DECIMAL", "ALPHA", "ROMAN")
            prefix = "  " * nesting + ("1." if is_ordered else "-")
            lines.append(f"{prefix} {text.strip()}")
            continue

        if not text.strip():
            lines.append("")
            continue

        lines.append(text)

    # Collapse consecutive blank lines into one
    result = []
    prev_blank = False
    for line in lines:
        is_blank = not line.strip()
        if is_blank and prev_blank:
            continue
        result.append(line)
        prev_blank = is_blank

    return "\n".join(result)


def cmd_auth(args):
    if len(args) < 1:
        print("Usage: fetch_gdoc.py auth <credentials.json> [token.json]", file=sys.stderr)
        sys.exit(1)
    creds_path = args[0]
    token_path = args[1] if len(args) > 1 else str(DEFAULT_TOKEN)
    _get_creds(creds_path, token_path)
    print(f"Authentication complete. Token saved to: {token_path}", file=sys.stderr)


def cmd_fetch(args):
    if len(args) < 2:
        print(
            "Usage: fetch_gdoc.py fetch <doc_id_or_url> <credentials.json> [token.json]",
            file=sys.stderr,
        )
        sys.exit(1)
    doc_id = _extract_doc_id(args[0])
    creds_path = args[1]
    token_path = args[2] if len(args) > 2 else str(DEFAULT_TOKEN)

    from googleapiclient.discovery import build  # noqa: PLC0415

    creds = _get_creds(creds_path, token_path)
    service = build("docs", "v1", credentials=creds)
    doc = service.documents().get(documentId=doc_id).execute()
    print(_doc_to_markdown(doc))


def main():
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(1)

    subcmd = sys.argv[1]
    rest = sys.argv[2:]

    if subcmd == "auth":
        cmd_auth(rest)
    elif subcmd == "fetch":
        cmd_fetch(rest)
    else:
        print(f"Unknown subcommand: {subcmd}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
