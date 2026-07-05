# Auth.md

## Agent authentication policy

`arleo.eu` exposes Hugo-published content through an MCP endpoint at https://mcp.arleo.eu/mcp.
Anonymous read-only access is available without registration. OAuth 2.0 unlocks richer tools.

## Agent registration

- **Registration endpoint**: https://mcp.arleo.eu/register
- **Authorization server**: https://mcp.arleo.eu
- **Authorization endpoint**: https://mcp.arleo.eu/authorize
- **Token endpoint**: https://mcp.arleo.eu/token
- **OAuth flow**: Authorization Code + PKCE (`S256` required)
- **Credential type**: Bearer token in `Authorization` header

### Standalone registration flow

```json
{
  "registration_flow": {
    "step_1_register": {
      "method": "POST",
      "url": "https://mcp.arleo.eu/register",
      "content_type": "application/json",
      "body": {
        "client_name": "<your-agent-name>",
        "redirect_uris": ["<your-redirect-uri>"],
        "grant_types": ["authorization_code"],
        "response_types": ["code"],
        "token_endpoint_auth_method": "none"
      },
      "returns": ["client_id", "client_secret"]
    },
    "step_2_authorize": {
      "method": "GET",
      "url": "https://mcp.arleo.eu/authorize",
      "params": {
        "response_type": "code",
        "client_id": "<client_id from step 1>",
        "redirect_uri": "<your-redirect-uri>",
        "scope": "content.read",
        "state": "<random-state>",
        "code_challenge": "<S256-pkce-challenge>",
        "code_challenge_method": "S256"
      }
    },
    "step_3_token": {
      "method": "POST",
      "url": "https://mcp.arleo.eu/token",
      "content_type": "application/x-www-form-urlencoded",
      "body": {
        "grant_type": "authorization_code",
        "code": "<authorization-code>",
        "redirect_uri": "<your-redirect-uri>",
        "client_id": "<client_id>",
        "code_verifier": "<pkce-verifier>"
      },
      "returns": ["access_token", "token_type", "expires_in"]
    },
    "step_4_use": {
      "method": "POST",
      "url": "https://mcp.arleo.eu/mcp",
      "headers": {
        "Authorization": "Bearer <access_token>",
        "Content-Type": "application/json"
      }
    }
  },
  "pkce": {
    "required": true,
    "method": "S256",
    "code_verifier_length": 43,
    "code_challenge": "BASE64URL(SHA256(code_verifier))"
  },
  "endpoints": {
    "registration_endpoint": "https://mcp.arleo.eu/register",
    "authorization_endpoint": "https://mcp.arleo.eu/authorize",
    "token_endpoint": "https://mcp.arleo.eu/token",
    "mcp_endpoint": "https://mcp.arleo.eu/mcp"
  },
  "scopes": ["content.read", "content.write", "site.admin"]
}
```

### Agent auth metadata

```json
{
  "agent_auth_metadata": {
    "skill": "https://mcp.arleo.eu/auth.md",
    "register_uri": "https://mcp.arleo.eu/register",
    "identity_endpoint": "https://mcp.arleo.eu/agent/identity",
    "claim_endpoint": "https://mcp.arleo.eu/agent/identity/claim",
    "claim_uri": "https://mcp.arleo.eu/agent/identity/claim",
    "events_endpoint": "https://mcp.arleo.eu/agent/event/notify",
    "identity_types_supported": ["anonymous", "identity_assertion"],
    "anonymous": {
      "credential_types_supported": ["none"],
      "claim_uri": "https://mcp.arleo.eu/agent/identity/claim"
    },
    "identity_assertion": {
      "assertion_types_supported": ["urn:ietf:params:oauth:token-type:id-jag"],
      "credential_types_supported": ["urn:ietf:params:oauth:token-type:id-jag"]
    },
    "events_supported": ["https://schemas.workos.com/events/agent/auth/identity/assertion/revoked"]
  }
}
```

## Scope

This document is public information only. It does not authorize private access.
