#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
SSH Deploy Slave

Usage:
  bash skills/ssh-deploy-slave/scripts/deploy.sh [options]

Required:
  --host <host>                 Target host (IP/DNS)
  --user <user>                 SSH user
  --remote-dir <dir>            Remote install dir (Linux: /opt/xinghebot, Windows: C:/opt/xinghebot)

SSH:
  --port <port>                 SSH port (default: 22)
  --key <path>                  SSH private key (recommended)
  --password-file <path>        Read SSH password from file (1st line). Requires sshpass. (optional)
  Password auth is supported via sshpass:
    - install sshpass
    - export SSHPASS='<password>'   (zsh: if contains "!" use single quotes)

Slave identity:
  --id <slave_id>               Stable slave id (optional)
  --name <name>                 Display name (optional)
  --tags <k=v,k=v>              Comma-separated tags (optional)
  --master <ws_url>             Master websocket url (required unless set in config start_params.slave.master)
  --heartbeat <duration>        Override heartbeat interval (optional; else use config/start_params or binary default)
  --max-inflight-runs <n>        Override max concurrent agent.run (optional; else use config/start_params or binary default)
  --insecure-skip-verify         Skip TLS cert verify for wss:// (dangerous; optional; else use config/start_params)

Sync options:
  --no-binary                   Do not upload the xinghebot binary
  --bin <path>                  Local xinghebot binary path (override auto selection)

  --remote-init                 Run remote `xinghebot slave --init` after uploading binary (default: on)
  --no-remote-init              Do not run remote init

  --sync-config                 Upload slave config (default: on)
  --no-sync-config              Do not upload slave config
  --config-src <path>           Local config file (default: ./slave-config.json)
  --config-dest <filename>      Remote config filename (default: slave-config.json)

  --sync-mcp                    Upload mcp.json (default: off)
  --mcp-src <path>              Local mcp.json path (default: ./mcp.json)
  --sync-mcp-runtime            Upload MCP runtime files (bin/ + mcp/ for built-in calculator) (default: off)

  --sync-skills                 Upload skills (default: off; use remote --init instead)
  --no-sync-skills              Do not upload skills
  --skills <a,b,c>              Skill dir names under ./skills (default: minimal set)
  --sync-all-skills             Upload ALL ./skills/* (default: off)

Start/verify:
  --no-start                    Do not start/restart the slave
  --restart                     Restart slave if already running (default: off)

Notes:
  - Auto binary selection detects the remote OS/arch and picks:
      - Linux:   dist/xinghebot-linux-{amd64,arm64}
      - Windows: dist/xinghebot-windows-{amd64,arm64}.exe
    If missing, it will suggest downloading the right binary (or build via `bash scripts/build_dist.sh`).
  - Default skills (minimal self-evolution set):
      skill-installer, skill-creator, mcp-builder, mcp-config-manager, ssh-deploy-slave
EOF
}

log() { printf '%s\n' "==> $*" >&2; }
die() { printf '%s\n' "error: $*" >&2; exit 1; }

HOST=""
USER=""
PORT="22"
KEY=""
PASSWORD_FILE=""
REMOTE_DIR=""
MASTER_URL=""
MASTER_URL_SET="false"

SLAVE_ID=""
SLAVE_NAME=""
SLAVE_TAGS=""
HEARTBEAT=""
HEARTBEAT_SET="false"
MAX_INFLIGHT_RUNS=""
MAX_INFLIGHT_RUNS_SET="false"
INSECURE_SKIP_VERIFY_SET="false"

SYNC_BINARY="true"
BIN_PATH=""

REMOTE_INIT="true"

SYNC_CONFIG="true"
CONFIG_SRC="./slave-config.json"
CONFIG_DEST="slave-config.json"

SYNC_MCP="false"
MCP_SRC="./mcp.json"
SYNC_MCP_RUNTIME="false"

SYNC_SKILLS="false"
SKILLS_CSV=""
SYNC_ALL_SKILLS="false"

START_SLAVE="true"
RESTART_SLAVE="false"

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    --host) HOST="${2:-}"; shift 2 ;;
    --user) USER="${2:-}"; shift 2 ;;
    --port) PORT="${2:-}"; shift 2 ;;
    --key) KEY="${2:-}"; shift 2 ;;
    --password-file) PASSWORD_FILE="${2:-}"; shift 2 ;;
    --remote-dir) REMOTE_DIR="${2:-}"; shift 2 ;;
    --master) MASTER_URL="${2:-}"; MASTER_URL_SET="true"; shift 2 ;;

    --id) SLAVE_ID="${2:-}"; shift 2 ;;
    --name) SLAVE_NAME="${2:-}"; shift 2 ;;
    --tags) SLAVE_TAGS="${2:-}"; shift 2 ;;
    --heartbeat) HEARTBEAT="${2:-}"; HEARTBEAT_SET="true"; shift 2 ;;
    --max-inflight-runs) MAX_INFLIGHT_RUNS="${2:-}"; MAX_INFLIGHT_RUNS_SET="true"; shift 2 ;;
    --insecure-skip-verify) INSECURE_SKIP_VERIFY_SET="true"; shift ;;

    --no-binary) SYNC_BINARY="false"; shift ;;
    --bin) BIN_PATH="${2:-}"; shift 2 ;;

    --remote-init) REMOTE_INIT="true"; shift ;;
    --no-remote-init) REMOTE_INIT="false"; shift ;;

    --sync-config) SYNC_CONFIG="true"; shift ;;
    --no-sync-config) SYNC_CONFIG="false"; shift ;;
    --config-src) CONFIG_SRC="${2:-}"; shift 2 ;;
    --config-dest) CONFIG_DEST="${2:-}"; shift 2 ;;

    --sync-mcp) SYNC_MCP="true"; shift ;;
    --mcp-src) MCP_SRC="${2:-}"; shift 2 ;;
    --sync-mcp-runtime) SYNC_MCP_RUNTIME="true"; shift ;;

    --sync-skills) SYNC_SKILLS="true"; shift ;;
    --no-sync-skills) SYNC_SKILLS="false"; shift ;;
    --skills) SKILLS_CSV="${2:-}"; shift 2 ;;
    --sync-all-skills) SYNC_ALL_SKILLS="true"; shift ;;

    --no-start) START_SLAVE="false"; shift ;;
    --restart) RESTART_SLAVE="true"; shift ;;
    *) die "unknown option: $1 (use --help)" ;;
  esac
done

[[ -n "${HOST// /}" ]] || die "--host is required"
[[ -n "${USER// /}" ]] || die "--user is required"
[[ -n "${REMOTE_DIR// /}" ]] || die "--remote-dir is required"
MASTER_URL_FROM_CONFIG_PARSED="false"
if [[ -z "${MASTER_URL// /}" ]] && [[ -f "${CONFIG_SRC}" ]] && command -v python3 >/dev/null 2>&1; then
  MASTER_URL_FROM_CONFIG_PARSED="true"
  MASTER_URL="$(python3 - "${CONFIG_SRC}" <<'PY' || true
import json
import sys

path = sys.argv[1]
try:
  with open(path, "r", encoding="utf-8") as f:
    cfg = json.load(f)
except Exception:
  sys.exit(0)

sp = cfg.get("start_params") or {}
slave = sp.get("slave") or {}
master = slave.get("master") or ""
if isinstance(master, str):
  print(master.strip())
PY
)"
fi
if [[ "${START_SLAVE}" == "true" ]] && [[ "${MASTER_URL_SET}" != "true" ]] && [[ -f "${CONFIG_SRC}" ]] && [[ "${MASTER_URL_FROM_CONFIG_PARSED}" == "true" ]] && [[ -z "${MASTER_URL// /}" ]]; then
  die "--master is required (or set it in ${CONFIG_SRC} at start_params.slave.master)"
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
cd "${ROOT_DIR}"

SSH_TARGET="${USER}@${HOST}"

SSH_BASE=(ssh -p "${PORT}")
SCP_BASE=(scp -P "${PORT}")
if [[ -n "${KEY// /}" ]]; then
  SSH_BASE+=(-i "${KEY}")
  SCP_BASE+=(-i "${KEY}")
fi

if [[ -n "${SSHPASS:-}" ]] && ! command -v sshpass >/dev/null 2>&1; then
  die "SSHPASS is set but sshpass is not installed. Install sshpass or use --key."
fi
if [[ -n "${PASSWORD_FILE// /}" ]]; then
  [[ -f "${PASSWORD_FILE}" ]] || die "--password-file not found: ${PASSWORD_FILE}"
  command -v sshpass >/dev/null 2>&1 || die "--password-file requires sshpass (install it or use --key)"
  SSH_BASE=(sshpass -f "${PASSWORD_FILE}" "${SSH_BASE[@]}")
  SCP_BASE=(sshpass -f "${PASSWORD_FILE}" "${SCP_BASE[@]}")
elif command -v sshpass >/dev/null 2>&1 && [[ -n "${SSHPASS:-}" ]]; then
  SSH_BASE=(sshpass -e "${SSH_BASE[@]}")
  SCP_BASE=(sshpass -e "${SCP_BASE[@]}")
fi

normalize_remote_dir_for_scp() {
  local d="$1"
  d="${d//\\//}"
  printf '%s' "${d}"
}

REMOTE_DIR_SCP="$(normalize_remote_dir_for_scp "${REMOTE_DIR}")"

ensure_ssh_auth() {
  # If stdin is not a TTY (e.g. running via an agent tool), interactive prompts will hang.
  if [[ -t 0 ]]; then
    return 0
  fi
  if [[ "${SSH_BASE[0]}" == "sshpass" ]]; then
    return 0
  fi

  local -a test_ssh
  test_ssh=(ssh -p "${PORT}")
  if [[ -n "${KEY// /}" ]]; then
    test_ssh+=(-i "${KEY}")
  fi
  test_ssh+=(-o BatchMode=yes -o ConnectTimeout=10)

  if ! "${test_ssh[@]}" "${SSH_TARGET}" "true" >/dev/null 2>&1; then
    die "SSH requires an interactive prompt, but stdin is not a TTY. Use --key, or install sshpass and set SSHPASS/--password-file."
  fi
}

detect_remote_os() {
  local sys
  sys="$("${SSH_BASE[@]}" "${SSH_TARGET}" "uname -s" 2>/dev/null || true)"
  sys="$(printf '%s' "${sys}" | tr -d '\r\n' | tr -d ' ')"
  if [[ -n "${sys}" ]]; then
    case "${sys}" in
      Linux|Darwin|FreeBSD|OpenBSD|NetBSD) echo "linux"; return 0 ;;
      MINGW*|MSYS*|CYGWIN*|Windows_NT) echo "windows"; return 0 ;;
    esac
  fi
  local ps
  ps="$("${SSH_BASE[@]}" "${SSH_TARGET}" "powershell.exe -NoProfile -NonInteractive -Command \"Write-Output windows\"" 2>/dev/null || true)"
  ps="$(printf '%s' "${ps}" | tr -d '\r\n' | tr -d ' ')"
  if [[ "${ps}" == "windows" ]]; then
    echo "windows"
    return 0
  fi
  echo "linux"
}

ensure_ssh_auth
REMOTE_OS="$(detect_remote_os)"

ps_encode() {
  local script="$1"
  if ! command -v iconv >/dev/null 2>&1; then
    die "iconv is required to run PowerShell commands (missing iconv)"
  fi
  if ! command -v base64 >/dev/null 2>&1; then
    die "base64 is required to run PowerShell commands (missing base64)"
  fi
  printf '%s' "${script}" | iconv -f UTF-8 -t UTF-16LE | base64 | tr -d '\r\n'
}

remote_exec_unix() {
  local cmd="$1"
  "${SSH_BASE[@]}" "${SSH_TARGET}" "bash -lc $(printf '%q' "${cmd}")"
}

remote_exec_win() {
  local script="$1"
  local encoded
  encoded="$(ps_encode "${script}")"
  "${SSH_BASE[@]}" "${SSH_TARGET}" "powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand ${encoded}"
}

win_ps_prelude() {
  local d
  d="$(printf '%s' "${REMOTE_DIR_SCP}" | sed "s/'/''/g")"
  cat <<EOF
\$ErrorActionPreference = 'Stop'
\$__input='${d}'
if (\$__input -match '^[A-Za-z]:[\\\\/]' -or \$__input.StartsWith('\\\\') -or \$__input.StartsWith('/')) {
  \$d = \$__input
} else {
  \$d = Join-Path \$HOME \$__input
}
EOF
}

remote_mkdirs() {
  if [[ "${REMOTE_OS}" == "windows" ]]; then
    remote_exec_win "$(win_ps_prelude)
New-Item -ItemType Directory -Force -Path \$d, (Join-Path \$d 'skills'), (Join-Path \$d 'bin'), (Join-Path \$d 'mcp') | Out-Null"
    return 0
  fi
  remote_exec_unix "mkdir -p '${REMOTE_DIR}' '${REMOTE_DIR}/skills' '${REMOTE_DIR}/bin' '${REMOTE_DIR}/mcp'"
}

remote_init() {
  log "remote init: xinghebot slave --init (config=${CONFIG_DEST})"
  if [[ "${REMOTE_OS}" == "windows" ]]; then
    remote_exec_win "$(win_ps_prelude)
\$exe = Join-Path \$d 'xinghebot.exe'
if (!(Test-Path -LiteralPath \$exe)) { Write-Error ('xinghebot.exe missing: ' + \$exe); exit 2 }
\$args = @(
  'slave',
  '--init',
  '--config', '${CONFIG_DEST}',
  '--skills-dir', 'skills',
  '--mcp-config', 'mcp.json'
)
& \$exe @args"
    return 0
  fi

  remote_exec_unix "cd '${REMOTE_DIR}' && test -x './xinghebot' && ./xinghebot slave --init --config '${CONFIG_DEST}' --skills-dir './skills' --mcp-config './mcp.json'"
}

detect_remote_arch() {
  local arch
  if [[ "${REMOTE_OS}" == "windows" ]]; then
    arch="$("${SSH_BASE[@]}" "${SSH_TARGET}" "powershell.exe -NoProfile -NonInteractive -Command \"Write-Output \$env:PROCESSOR_ARCHITECTURE\"" 2>/dev/null || true)"
    arch="$(printf '%s' "${arch}" | tr -d '\r\n' | tr -d ' ')"
    case "${arch}" in
      AMD64|X64) echo "amd64" ;;
      ARM64) echo "arm64" ;;
      *) die "unsupported remote arch from PROCESSOR_ARCHITECTURE: ${arch}" ;;
    esac
    return 0
  fi

  arch="$("${SSH_BASE[@]}" "${SSH_TARGET}" "uname -m" 2>/dev/null | tr -d '\r\n' | tr -d ' ')"
  case "${arch}" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *) die "unsupported remote arch from uname -m: ${arch}" ;;
  esac
}

select_local_bin() {
  if [[ -n "${BIN_PATH// /}" ]]; then
    [[ -f "${BIN_PATH}" ]] || die "--bin not found: ${BIN_PATH}"
    echo "${BIN_PATH}"
    return 0
  fi
  local goarch
  goarch="$(detect_remote_arch)"
  local candidate=""
  local legacy=""
  if [[ "${REMOTE_OS}" == "windows" ]]; then
    candidate="dist/xinghebot-windows-${goarch}.exe"
    legacy="dist/agent-windows-${goarch}.exe"
  else
    candidate="dist/xinghebot-linux-${goarch}"
    legacy="dist/agent-linux-${goarch}"
  fi
  if [[ -f "${candidate}" ]]; then
    echo "${candidate}"
    return 0
  fi
  if [[ -f "${legacy}" ]]; then
    log "using legacy binary name: ${legacy} (consider rebuilding to dist/xinghebot-*)"
    echo "${legacy}"
    return 0
  fi

  log "missing ${candidate}"
  log "note: cross-platform deploy requires the matching binary in ./dist/ (download it or build via: bash scripts/build_dist.sh)"
  command -v go >/dev/null 2>&1 || die "go is not installed, cannot build ${candidate}. Download the correct binary into ./dist/ and re-run."
  log "building dist..."
  bash scripts/build_dist.sh
  [[ -f "${candidate}" ]] || die "build finished but binary still missing: ${candidate}"
  echo "${candidate}"
}

sync_binary() {
  local src="$1"
  if [[ "${REMOTE_OS}" == "windows" ]]; then
    log "upload xinghebot binary: ${src} -> ${REMOTE_DIR_SCP}/xinghebot.exe"
    "${SCP_BASE[@]}" "${src}" "${SSH_TARGET}:${REMOTE_DIR_SCP}/xinghebot.exe.tmp"
    remote_exec_win "$(win_ps_prelude)
Move-Item -LiteralPath (Join-Path \$d 'xinghebot.exe.tmp') -Destination (Join-Path \$d 'xinghebot.exe') -Force"
    return 0
  fi

  log "upload xinghebot binary: ${src} -> ${REMOTE_DIR}/xinghebot"
  "${SCP_BASE[@]}" "${src}" "${SSH_TARGET}:${REMOTE_DIR}/xinghebot.tmp"
  remote_exec_unix "chmod +x '${REMOTE_DIR}/xinghebot.tmp' && mv -f '${REMOTE_DIR}/xinghebot.tmp' '${REMOTE_DIR}/xinghebot'"
}

sync_config() {
  local src="$1"
  [[ -f "${src}" ]] || die "config src not found: ${src}"
  log "upload config: ${src} -> ${REMOTE_DIR}/${CONFIG_DEST}"
  "${SCP_BASE[@]}" "${src}" "${SSH_TARGET}:${REMOTE_DIR_SCP}/${CONFIG_DEST}"
}

sync_mcp_config() {
  local src="$1"
  [[ -f "${src}" ]] || die "mcp src not found: ${src}"
  log "upload mcp.json: ${src} -> ${REMOTE_DIR}/mcp.json"
  "${SCP_BASE[@]}" "${src}" "${SSH_TARGET}:${REMOTE_DIR_SCP}/mcp.json"
}

sync_skills() {
  local -a skills
  if [[ "${SYNC_ALL_SKILLS}" == "true" ]]; then
    skills=()
    while IFS= read -r d; do
      [[ -n "${d}" ]] || continue
      skills+=("$(basename "${d}")")
    done < <(find skills -mindepth 1 -maxdepth 1 -type d -print | sort)
  elif [[ -n "${SKILLS_CSV// /}" ]]; then
    IFS=',' read -r -a skills <<<"${SKILLS_CSV}"
  else
    skills=("skill-installer" "skill-creator" "mcp-builder" "mcp-config-manager" "ssh-deploy-slave")
  fi

  local -a paths
  paths=()
  for s in "${skills[@]}"; do
    s="$(echo "${s}" | xargs)"
    [[ -n "${s}" ]] || continue
    [[ -d "skills/${s}" ]] || die "skill dir not found: skills/${s}"
    paths+=("skills/${s}")
  done
  [[ ${#paths[@]} -gt 0 ]] || die "no skills to sync"

  log "upload skills -> ${REMOTE_DIR}/skills (${#paths[@]} dirs)"
  if [[ "${REMOTE_OS}" == "windows" ]]; then
    for p in "${paths[@]}"; do
      log "upload skill dir: ${p}"
      "${SCP_BASE[@]}" -r "${p}" "${SSH_TARGET}:${REMOTE_DIR_SCP}/skills/"
    done
    return 0
  fi

  tar -czf - "${paths[@]}" | "${SSH_BASE[@]}" "${SSH_TARGET}" "mkdir -p '${REMOTE_DIR}' && tar -xzf - -C '${REMOTE_DIR}'"
}

sync_mcp_runtime() {
  local -a items
  items=(
    "bin/calculator-mcp"
    "bin/setup_calculator.py"
    "mcp/README.md"
    "mcp/calculator"
  )
  for it in "${items[@]}"; do
    [[ -e "${it}" ]] || die "missing MCP runtime item: ${it}"
  done

  log "upload MCP runtime -> ${REMOTE_DIR} (built-in calculator)"
  if [[ "${REMOTE_OS}" == "windows" ]]; then
    local tmp_zip
    tmp_zip="$(mktemp -t mcp-runtime.XXXXXX.zip)"
    python3 - "${tmp_zip}" <<'PY'
import os
import sys
import zipfile

zip_path = sys.argv[1]
items = [
    "bin/calculator-mcp",
    "bin/setup_calculator.py",
    "mcp/README.md",
    "mcp/calculator",
]
exclude_dirs = {
    os.path.normpath("mcp/calculator/venv"),
    os.path.normpath("mcp/calculator/__pycache__"),
}

def should_skip_dir(dirpath: str) -> bool:
    nd = os.path.normpath(dirpath)
    for ex in exclude_dirs:
        if nd == ex:
            return True
        if nd.startswith(ex + os.sep):
            return True
    return False

with zipfile.ZipFile(zip_path, "w", compression=zipfile.ZIP_DEFLATED) as z:
    for item in items:
        if os.path.isdir(item):
            for root, dirs, files in os.walk(item):
                if should_skip_dir(root):
                    dirs[:] = []
                    continue
                dirs[:] = [d for d in dirs if not should_skip_dir(os.path.join(root, d))]
                for fn in files:
                    if fn.endswith(".pyc"):
                        continue
                    p = os.path.join(root, fn)
                    arc = p.replace(os.sep, "/")
                    z.write(p, arcname=arc)
        else:
            z.write(item, arcname=item.replace(os.sep, "/"))
PY
    log "upload zip -> ${REMOTE_DIR_SCP}/mcp-runtime.zip"
    "${SCP_BASE[@]}" "${tmp_zip}" "${SSH_TARGET}:${REMOTE_DIR_SCP}/mcp-runtime.zip"
    rm -f "${tmp_zip}"
    remote_exec_win "$(win_ps_prelude)
Expand-Archive -LiteralPath (Join-Path \$d 'mcp-runtime.zip') -DestinationPath \$d -Force
Remove-Item -LiteralPath (Join-Path \$d 'mcp-runtime.zip') -Force"
    return 0
  fi

  tar \
    --exclude='mcp/calculator/venv' \
    --exclude='mcp/calculator/__pycache__' \
    --exclude='mcp/calculator/*.pyc' \
    -czf - "${items[@]}" \
    | "${SSH_BASE[@]}" "${SSH_TARGET}" "mkdir -p '${REMOTE_DIR}' && tar -xzf - -C '${REMOTE_DIR}'"
}

start_slave() {
  if [[ "${REMOTE_OS}" == "windows" ]]; then
    if ! remote_exec_win "$(win_ps_prelude)
if (!(Test-Path -LiteralPath (Join-Path \$d '${CONFIG_DEST}'))) { exit 2 }"; then
      die "remote config missing: ${REMOTE_DIR_SCP}/${CONFIG_DEST} (use --sync-config or upload it manually)"
    fi

    local stop_ps=""
    if [[ "${RESTART_SLAVE}" == "true" ]]; then
      stop_ps="\$pidPath=Join-Path \$d 'slave.pid'; if (Test-Path -LiteralPath \$pidPath) { \$pid=(Get-Content -LiteralPath \$pidPath | Select-Object -First 1); if (\$pid -match '^[0-9]+$') { try { Stop-Process -Id [int]\$pid -Force -ErrorAction SilentlyContinue } catch {} } }"
    fi

    remote_exec_win "$(win_ps_prelude)
${stop_ps}
\$exe = Join-Path \$d 'xinghebot.exe'
if (!(Test-Path -LiteralPath \$exe)) { Write-Error ('xinghebot.exe missing: ' + \$exe); exit 2 }
\$args = @(
  'slave',
  '--config', '${CONFIG_DEST}',
  '--skills-dir', 'skills',
  '--mcp-config', 'mcp.json'
)
if ('${MASTER_URL_SET}' -eq 'true') { \$args += @('--master', '${MASTER_URL}') }
if ('${HEARTBEAT_SET}' -eq 'true') { \$args += @('--heartbeat', '${HEARTBEAT}') }
if ('${MAX_INFLIGHT_RUNS_SET}' -eq 'true') { \$args += @('--max-inflight-runs', '${MAX_INFLIGHT_RUNS}') }
if ('${SLAVE_ID}' -ne '')   { \$args += @('--id',   '${SLAVE_ID}') }
if ('${SLAVE_NAME}' -ne '') { \$args += @('--name', '${SLAVE_NAME}') }
if ('${SLAVE_TAGS}' -ne '') { \$args += @('--tags', '${SLAVE_TAGS}') }
if ('${INSECURE_SKIP_VERIFY_SET}' -eq 'true') { \$args += @('--insecure-skip-verify') }
\$log = Join-Path \$d 'slave.log'
\$p = Start-Process -FilePath \$exe -ArgumentList \$args -WorkingDirectory \$d -RedirectStandardOutput \$log -RedirectStandardError \$log -PassThru
\$p.Id | Out-File -LiteralPath (Join-Path \$d 'slave.pid') -Encoding ascii
Start-Sleep -Milliseconds 300
Write-Output ('pid=' + \$p.Id)
try { Get-Process -Id \$p.Id | Select-Object -First 1 Id,ProcessName,Path } catch {}"
    log "remote log: ${REMOTE_DIR_SCP}/slave.log"
    return 0
  fi

  local remote_config="./${CONFIG_DEST}"
  remote_exec_unix "test -f '${REMOTE_DIR}/${CONFIG_DEST}'" || die "remote config missing: ${REMOTE_DIR}/${CONFIG_DEST} (use --sync-config or upload it manually)"

  local -a args
  args=( "./xinghebot" "slave"
    "--config" "${remote_config}"
    "--skills-dir" "./skills"
    "--mcp-config" "./mcp.json"
  )
  if [[ "${MASTER_URL_SET}" == "true" ]]; then
    args+=( "--master" "${MASTER_URL}" )
  fi
  if [[ "${HEARTBEAT_SET}" == "true" ]]; then
    args+=( "--heartbeat" "${HEARTBEAT}" )
  fi
  if [[ "${MAX_INFLIGHT_RUNS_SET}" == "true" ]]; then
    args+=( "--max-inflight-runs" "${MAX_INFLIGHT_RUNS}" )
  fi
  if [[ -n "${SLAVE_ID// /}" ]]; then
    args+=( "--id" "${SLAVE_ID}" )
  fi
  if [[ -n "${SLAVE_NAME// /}" ]]; then
    args+=( "--name" "${SLAVE_NAME}" )
  fi
  if [[ -n "${SLAVE_TAGS// /}" ]]; then
    args+=( "--tags" "${SLAVE_TAGS}" )
  fi
  if [[ "${INSECURE_SKIP_VERIFY_SET}" == "true" ]]; then
    args+=( "--insecure-skip-verify" )
  fi

  local stop_snippet=""
  if [[ "${RESTART_SLAVE}" == "true" ]]; then
    stop_snippet=$'if [ -f slave.pid ]; then\n  pid="$(cat slave.pid 2>/dev/null || true)"\n  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then\n    kill "$pid" || true\n    sleep 1\n  fi\nfi\n'
  fi

  log "start slave (nohup) in ${REMOTE_DIR}"
  remote_exec_unix "cd '${REMOTE_DIR}' && ${stop_snippet}nohup ${args[*]} > slave.log 2>&1 & echo \$! > slave.pid"
  remote_exec_unix "cd '${REMOTE_DIR}' && echo 'pid='$(cat slave.pid) && ps -p $(cat slave.pid) -o pid,cmd || true"
  log "remote log: ${REMOTE_DIR}/slave.log"
}

log "target: ${SSH_TARGET} (port=${PORT})"
remote_mkdirs

if [[ "${SYNC_BINARY}" == "true" ]]; then
  bin="$(select_local_bin)"
  sync_binary "${bin}"
fi

if [[ "${REMOTE_INIT}" == "true" ]]; then
  remote_init
fi

if [[ "${SYNC_CONFIG}" == "true" ]]; then
  sync_config "${CONFIG_SRC}"
fi

if [[ "${SYNC_MCP}" == "true" ]]; then
  sync_mcp_config "${MCP_SRC}"
fi

if [[ "${SYNC_MCP_RUNTIME}" == "true" ]]; then
  sync_mcp_runtime
fi

if [[ "${SYNC_SKILLS}" == "true" ]]; then
  sync_skills
fi

if [[ "${START_SLAVE}" == "true" ]]; then
  start_slave
else
  log "skipped starting slave (--no-start)"
fi
