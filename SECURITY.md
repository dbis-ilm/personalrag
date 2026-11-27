# Security policy

PersonalRAG indexes personal documents and gives agents access to them, so security reports are welcome and taken seriously.

## Reporting a vulnerability

Please **do not open a public issue** for a security problem. Report it privately through GitHub instead:

1. Go to the repository's **Security** tab.
2. Choose **Report a vulnerability**.

This opens a private advisory that only you and the maintainer can see, where a fix can be prepared before anything is published. Please include the version or commit, your platform, the steps to reproduce, and what an attacker could gain.

You will get a first answer within a week. This is a small project maintained in spare time, so a fix may take longer; you will be kept informed, and credited in the advisory if you wish.

## Supported versions

Before version 1.0 only the latest state of `main` is supported. Fixes are not backported.

## Scope

In scope, for example:

- Agents (MCP clients) reading collections or documents the user did not share, or reaching anything beyond the read-only tools
- Document content that makes PersonalRAG itself perform actions, or that leaks data through its answers or tool results (prompt injection)
- Document excerpts, conversations or API keys leaving the machine without the user's consent, e.g. to a remote model server the user did not approve
- API keys stored anywhere other than the system keychain, or exposed in logs or diagnostic reports
- Files being changed or deleted by PersonalRAG; it must never modify indexed documents

Out of scope:

- The behaviour of the language models themselves (wrong or invented answers)
- Vulnerabilities in Ollama, GPUStack, LM Studio or other model servers; please report those to their projects
- Attacks that require an attacker who already runs code as the same user on the same machine; the index is protected like the rest of the user's profile
- What an agent's own model provider does with text the agent read through `read_document`; the app documents this trade-off

## Current attack surface

Agents reach PersonalRAG only over stdio: the client starts `PersonalRAG --mcp` as a child process. There is no network listener yet. An HTTP endpoint for the running app is planned; it will bind to `127.0.0.1` only and require a token and an origin check.
