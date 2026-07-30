# Local development scripts

These Node scripts manage the local Go backend and Vite frontend together.

## Start

```powershell
node .\scripts\dev-start.mjs
```

Defaults:

- Backend: `http://127.0.0.1:8418`
- Frontend: `http://127.0.0.1:3010`
- SQLite database: `.\data\upstream-ops.db`
- Config file: `.\config.yaml`
- Local auth: disabled with `AUTH_ENABLED=false`
- Runtime state and logs: `.\tmp\dev\`

## Stop

```powershell
node .\scripts\dev-stop.mjs
```

If the PID file is stale or missing but ports are still occupied, stop by recorded PID plus local ports:

```powershell
node .\scripts\dev-stop.mjs --by-port
```

## Restart

```powershell
node .\scripts\dev-restart.mjs
```

## Common options

```powershell
# Backend only
node .\scripts\dev-start.mjs --backend-only

# Frontend only
node .\scripts\dev-start.mjs --frontend-only

# Use a different APP_SECRET when the database was encrypted with another key
node .\scripts\dev-start.mjs --app-secret "your-existing-secret"

# Skip automatic pnpm install when frontend/node_modules is missing
node .\scripts\dev-start.mjs --no-install
```
