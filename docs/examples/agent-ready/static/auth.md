# Auth.md

## Agent authentication policy

`arleo.eu` exposes Hugo-published content through an MCP endpoint at https://mcp.arleo.eu/mcp.
Anonymous read-only access is available without registration when `oauth.enabled`
is `false`. When OAuth is enabled, all `/mcp` requests require a Bearer token
(the server returns `401` otherwise). Complete the PKCE flow once to obtain a
token with `read` scope for read-only access (full visibility, including
drafts). OAuth 2.0 unlocks richer tools.

## Agent registration

Canonical runtime scopes:

- `read`: self-serve OAuth registration, full read visibility including drafts/source-only content
- `write`: approved client, `read` plus mutations and site operations

Descriptive external profile labels still used in some docs:

- `reader`: human-facing label for the `read` scope
- `operator`: human-facing label for the `write` scope

The OAuth scopes below are the current internal capability strings accepted by
the server during v1.x.

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
        "grant_types": ["authorization_code", "refresh_token"],
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
        "scope": "read",
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
      "returns": ["access_token", "token_type", "expires_in", "refresh_token", "refresh_expires_in"]
    },
    "step_3b_refresh": {
      "method": "POST",
      "url": "https://mcp.arleo.eu/token",
      "content_type": "application/x-www-form-urlencoded",
      "body": {
        "grant_type": "refresh_token",
        "refresh_token": "<refresh-token>",
        "client_id": "<client_id>"
      },
      "returns": ["access_token", "token_type", "expires_in", "refresh_token", "refresh_expires_in"]
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
  "scopes": ["read", "write"],
  "access_profiles": {
    "reader": {
      "description": "Human-facing label for the canonical `read` scope: discovery and content inspection with full visibility, drafts included.",
      "acquisition": "self-serve OAuth registration",
      "internal_scopes": ["read"]
    },
    "operator": {
      "description": "Human-facing label for the canonical `write` scope: full read visibility plus write and site operation capabilities.",
      "acquisition": "approved OAuth client/token present in the server registry",
      "internal_scopes": ["write"]
    }
  }
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
