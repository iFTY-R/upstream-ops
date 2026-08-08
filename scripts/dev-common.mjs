import { spawn, spawnSync } from "node:child_process"
import fs from "node:fs"
import net from "node:net"
import os from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"

const __dirname = path.dirname(fileURLToPath(import.meta.url))

export const repoRoot = path.resolve(__dirname, "..")
export const stateDir = path.join(repoRoot, "tmp", "dev")
export const pidFile = path.join(stateDir, "pids.json")

const frontendDir = path.join(repoRoot, "frontend")

export function parseCommonArgs(argv) {
  const options = {
    backendOnly: false,
    frontendOnly: false,
    noInstall: false,
    byPort: false,
    databasePath: "./data/upstream-ops.db",
    configPath: "./config.yaml",
    appSecret: "KgYoAVXF/O1JRBTQnY05EWBzNDSWchWfsmBDI6EHZn6+WcYalPcWYud1uG8/mHNt",
    backendPort: 8418,
    frontendPort: 3010,
    frontendHost: "127.0.0.1",
  }

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index]
    const [name, inlineValue] = arg.startsWith("--") ? arg.split("=", 2) : [arg, undefined]
    const nextValue = () => {
      if (inlineValue !== undefined) {
        return inlineValue
      }
      index += 1
      if (index >= argv.length) {
        throw new Error(`Missing value for ${name}`)
      }
      return argv[index]
    }

    switch (name) {
      case "--backend-only":
        options.backendOnly = true
        break
      case "--frontend-only":
        options.frontendOnly = true
        break
      case "--no-install":
        options.noInstall = true
        break
      case "--by-port":
        options.byPort = true
        break
      case "--database-path":
        options.databasePath = nextValue()
        break
      case "--config-path":
        options.configPath = nextValue()
        break
      case "--app-secret":
        options.appSecret = nextValue()
        break
      case "--backend-port":
        options.backendPort = Number.parseInt(nextValue(), 10)
        break
      case "--frontend-port":
        options.frontendPort = Number.parseInt(nextValue(), 10)
        break
      case "--frontend-host":
        options.frontendHost = nextValue()
        break
      default:
        throw new Error(`Unknown option: ${arg}`)
    }
  }

  if (options.backendOnly && options.frontendOnly) {
    throw new Error("--backend-only and --frontend-only cannot be used together")
  }
  if (!Number.isInteger(options.backendPort) || options.backendPort <= 0) {
    throw new Error("--backend-port must be a positive integer")
  }
  if (!Number.isInteger(options.frontendPort) || options.frontendPort <= 0) {
    throw new Error("--frontend-port must be a positive integer")
  }

  return options
}

export function ensureDevDirectories() {
  fs.mkdirSync(stateDir, { recursive: true })
  fs.mkdirSync(path.join(repoRoot, "data"), { recursive: true })
}

export function readState() {
  if (!fs.existsSync(pidFile)) {
    return {}
  }

  const raw = fs.readFileSync(pidFile, "utf8").replace(/^\uFEFF/, "")
  if (!raw.trim()) {
    return {}
  }

  try {
    return JSON.parse(raw)
  } catch {
    console.warn(`Ignoring unreadable PID state file: ${pidFile}`)
    return {}
  }
}

export function writeState(state) {
  ensureDevDirectories()
  fs.writeFileSync(pidFile, `${JSON.stringify(state, null, 2)}\n`, "utf8")
}

export function assertCommand(command, versionArgs = ["--version"]) {
  const spawnTarget = buildSpawnTarget(command, versionArgs)
  const result = spawnSync(spawnTarget.command, spawnTarget.args, {
    cwd: repoRoot,
    stdio: "ignore",
    windowsHide: true,
  })
  if (result.error || result.status !== 0) {
    throw new Error(`Missing required command: ${command}`)
  }
}

export function isProcessRunning(processId) {
  if (!processId) {
    return false
  }

  try {
    process.kill(Number(processId), 0)
    return true
  } catch {
    return false
  }
}

export async function startDev(options) {
  ensureDevDirectories()

  const startBackend = !options.frontendOnly
  const startFrontend = !options.backendOnly
  const state = readState()

  if (startBackend) {
    assertCommand("go", ["version"])
  }
  if (startFrontend) {
    assertCommand("pnpm", ["--version"])
  }

  if (startBackend) {
    startService({
      name: "backend",
      command: "go",
      args: ["run", "./cmd/server", "-config", options.configPath],
      cwd: repoRoot,
      env: {
        APP_SECRET: options.appSecret,
        AUTH_ENABLED: "false",
        DATABASE_DRIVER: "sqlite",
        DATABASE_PATH: options.databasePath,
        SERVER_PORT: String(options.backendPort),
      },
      label: `go run ./cmd/server -config ${options.configPath}`,
      state,
    })
  }

  if (startFrontend) {
    const nodeModules = path.join(frontendDir, "node_modules")
    if (!options.noInstall && !fs.existsSync(nodeModules)) {
      runForeground("pnpm", ["install"], frontendDir)
    }

    startService({
      name: "frontend",
      command: "pnpm",
      args: [
        "exec",
        "vite",
        "--host",
        options.frontendHost,
        "--port",
        String(options.frontendPort),
        "--strictPort",
      ],
      cwd: frontendDir,
      env: {
        VITE_BACKEND_URL: `http://localhost:${options.backendPort}`,
        BROWSER: "none",
      },
      label: `pnpm exec vite --host ${options.frontendHost} --port ${options.frontendPort} --strictPort`,
      state,
    })
  }

  writeState(state)
  console.log("")
  console.log(`Backend:  http://127.0.0.1:${options.backendPort}`)
  console.log(`Frontend: http://${options.frontendHost}:${options.frontendPort}`)
  console.log(`Database: ${options.databasePath}`)
  console.log(`Logs:     ${stateDir}`)
}

export async function stopDev(options) {
  ensureDevDirectories()

  const stopBackend = !options.frontendOnly
  const stopFrontend = !options.backendOnly
  const state = readState()

  if (stopBackend) {
    stopRecordedService("backend", state)
  }
  if (stopFrontend) {
    stopRecordedService("frontend", state)
  }

  writeState(state)

  if (options.byPort) {
    if (stopBackend) {
      stopProcessesByPort(options.backendPort, "backend")
    }
    if (stopFrontend) {
      stopProcessesByPort(options.frontendPort, "frontend")
    }
  }

  console.log("Dev environment stopped")
}

function startService({ name, command, args, cwd, env, label, state }) {
  const recordedPid = state[name]?.pid
  if (isProcessRunning(recordedPid)) {
    console.log(`${name} already running, PID ${recordedPid}`)
    return
  }

  const stdoutPath = path.join(stateDir, `${name}.log`)
  const stderrPath = path.join(stateDir, `${name}.err.log`)
  const stdout = fs.openSync(stdoutPath, "w")
  const stderr = fs.openSync(stderrPath, "w")
  const spawnTarget = buildSpawnTarget(command, args)

  try {
    const child = spawn(spawnTarget.command, spawnTarget.args, {
      cwd,
      env: { ...process.env, ...env },
      detached: true,
      stdio: ["ignore", stdout, stderr],
      windowsHide: true,
    })
    child.unref()

    state[name] = {
      pid: child.pid,
      startedAt: new Date().toISOString(),
      command: label,
      stdout: stdoutPath,
      stderr: stderrPath,
    }
    console.log(`${name} started, PID ${child.pid}`)
  } finally {
    fs.closeSync(stdout)
    fs.closeSync(stderr)
  }
}

function runForeground(command, args, cwd) {
  const spawnTarget = buildSpawnTarget(command, args)
  const result = spawnSync(spawnTarget.command, spawnTarget.args, {
    cwd,
    stdio: "inherit",
    windowsHide: true,
  })
  if (result.error) {
    throw result.error
  }
  if (result.status !== 0) {
    throw new Error(`${command} ${args.join(" ")} failed with exit code ${result.status}`)
  }
}

function stopRecordedService(name, state) {
  const pid = Number(state[name]?.pid)
  if (!pid) {
    console.log(`${name} not recorded`)
    return
  }

  killProcessTree(pid, name)
  delete state[name]
}

function stopProcessesByPort(port, name) {
  const pids = findListeningPids(port)
  for (const pid of pids) {
    killProcessTree(pid, `${name} port ${port}`)
  }
}

function killProcessTree(pid, name) {
  if (!isProcessRunning(pid)) {
    console.log(`${name} not running, stale PID ${pid}`)
    return
  }

  if (isWindows()) {
    const result = spawnSync("taskkill", ["/PID", String(pid), "/T", "/F"], {
      stdio: "ignore",
      windowsHide: true,
    })
    if (result.status !== 0 && isProcessRunning(pid)) {
      throw new Error(`Failed to stop ${name}, PID ${pid}`)
    }
  } else {
    try {
      process.kill(-pid, "SIGTERM")
    } catch {
      process.kill(pid, "SIGTERM")
    }
  }

  console.log(`${name} stopped, PID ${pid}`)
}

function findListeningPids(port) {
  if (isWindows()) {
    const result = spawnSync("netstat", ["-ano", "-p", "tcp"], {
      encoding: "utf8",
      windowsHide: true,
    })
    if (result.error || result.status !== 0) {
      return []
    }

    const pids = new Set()
    for (const line of result.stdout.split(/\r?\n/)) {
      if (!line.includes("LISTENING")) {
        continue
      }
      const parts = line.trim().split(/\s+/)
      const localAddress = parts[1] ?? ""
      const pid = Number(parts[4])
      if (localAddress.endsWith(`:${port}`) && Number.isInteger(pid)) {
        pids.add(pid)
      }
    }
    return [...pids]
  }

  const result = spawnSync("lsof", ["-ti", `tcp:${port}`, "-sTCP:LISTEN"], {
    encoding: "utf8",
  })
  if (result.error || result.status !== 0) {
    return []
  }
  return result.stdout
    .split(/\s+/)
    .map((value) => Number(value))
    .filter((value) => Number.isInteger(value) && value > 0)
}

function buildSpawnTarget(command, args) {
  if (!isWindows()) {
    return { command, args }
  }

  return {
    command: "cmd.exe",
    args: ["/d", "/s", "/c", quoteWindowsCommand([command, ...args])],
  }
}

function quoteWindowsCommand(parts) {
  return parts.map(quoteWindowsArg).join(" ")
}

function quoteWindowsArg(value) {
  const text = String(value)
  if (text.length > 0 && !/[\s"&|<>^]/.test(text)) {
    return text
  }
  return `"${text.replace(/(\\*)"/g, '$1$1\\"').replace(/(\\+)$/g, "$1$1")}"`
}

export function waitForHttp(url, timeoutMs = 30000) {
  return new Promise((resolve, reject) => {
    const deadline = Date.now() + timeoutMs

    const attempt = () => {
      const socket = net.connect(new URL(url).port, new URL(url).hostname)
      socket.once("connect", () => {
        socket.destroy()
        resolve()
      })
      socket.once("error", () => {
        socket.destroy()
        if (Date.now() >= deadline) {
          reject(new Error(`Timed out waiting for ${url}`))
          return
        }
        setTimeout(attempt, 500)
      })
    }

    attempt()
  })
}

function isWindows() {
  return os.platform() === "win32"
}
