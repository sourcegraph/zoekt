# MCP Local Testing

End-to-end guide to test the MCP OAuth flow locally using Docker.

## Prerequisites

- Docker
- OKTA_BASE_URL
- `ZOEKT_OKTA_CLIENT_ID` — the Okta app client ID

## Variables

Adjust these to your environment:

```bash
REPO_TO_INDEX=~/repositories/zoekt   # local repo to index
INDEX_VOLUME=zoekt-index-test         # Docker volume name for the index
OKTA_BASE_URL=<your_okta_base_url>
ZOEKT_OKTA_CLIENT_ID=<your_client_id>
CALLBACK_PORT=9877                    # must be registered as redirect URI in Okta app
```

## 1. Build the image

```bash
cd ~/repositories/zoekt
docker build -f Dockerfile -t zoekt-test .
```

## 2. Index a local repository (one-shot)

```bash
docker run --rm \
  -v ${INDEX_VOLUME}:/data/index \
  -v ${REPO_TO_INDEX}:/repo \
  --entrypoint zoekt-git-index \
  zoekt-test \
  -index /data/index /repo
```

This writes index files into the `${INDEX_VOLUME}` Docker volume. Re-run whenever the repo changes.

## 3. Start the server

```bash
docker run --rm -it \
  -p 8080:8080 \
  -e ZOEKT_OKTA_BASE_URL=${OKTA_BASE_URL} \
  -e SRC_LOG_LEVEL=info \
  -v ${INDEX_VOLUME}:/data/index \
  --entrypoint zoekt-webserver \
  zoekt-test \
  -index /data/index -listen :8080
```

## 4. Verify the server is up

```bash
# OAuth discovery endpoint — should return Okta metadata
curl http://localhost:8080/.well-known/oauth-authorization-server | jq .issuer

# MCP endpoint without token — should return 401
curl http://localhost:8080/mcp
```

## 5. Register the MCP server in Claude Code

```bash
  claude mcp add-json zoekt-search \
  '{"type":"http","url":"http://localhost:8080/mcp","oauth":{"clientId":"'${ZOEKT_OKTA_CLIENT_ID}'","callbackPort":'${CALLBACK_PORT}',"scopes":"openid profile offline_access"}}
```

Click **Authenticate** in Claude Code to trigger the Okta PKCE flow. Once authenticated, the `zoekt_search` tool is available.

## 6. Test the zoekt_search tool

In Claude Code, run:

```
use zoekt search r:zoekt file:README
```

You should get results listing README files from the indexed repository.

To remove the MCP server:

```bash
claude mcp remove zoekt-search
```