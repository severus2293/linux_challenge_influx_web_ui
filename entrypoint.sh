#!/bin/bash
set -eo pipefail

# Логирование в стиле официального скрипта
declare -rA LOG_LEVELS=( [error]=0 [warn]=1 [info]=2 [debug]=3 )
declare LOG_LEVEL=error

# Mimic the structured logging used by InfluxDB.
# Usage: log <level> <msg> [<key> <val>]...
function log () {
    local -r level=$1 msg=$2
    shift 2

    if [ "${LOG_LEVELS[${level}]}" -gt "${LOG_LEVELS[${LOG_LEVEL}]}" ]; then
        return
    fi

    local attrs='"system": "docker"'
    while [ "$#" -gt 1 ]; do
        attrs="${attrs}, \"$1\": \"$2\""
        shift 2
    done

    local -r logtime="$(date --utc +'%FT%T.%NZ')"
    1>&2 echo -e "${logtime}\t${level}\t${msg}\t{${attrs}}"
}

# Set the global log-level for the entry-point to match the config passed to influxd.
function set_global_log_level () {
    local level="$(influxd::config::get log-level "${@}")"

    if [ -z "${level}" ] || [ -z "${LOG_LEVELS[${level}]}" ]; then
      LOG_LEVEL=info
    else
      LOG_LEVEL=${level}
    fi
}

# Look for standard config names in the volume configured in our Dockerfile.
declare -r CONFIG_VOLUME=/etc/influxdb2
declare -ra CONFIG_NAMES=(config.json config.toml config.yaml config.yml)

# Search for a V2 config file, and export its path into the env for influxd to use.
function set_config_path () {
    local config_path=/etc/defaults/influxdb2/config.yml

    if [ -n "$INFLUXD_CONFIG_PATH" ]; then
        config_path="${INFLUXD_CONFIG_PATH}"
    else
        for name in "${CONFIG_NAMES[@]}"; do
            if [ -f "${CONFIG_VOLUME}/${name}" ]; then
                config_path="${CONFIG_VOLUME}/${name}"
                break
            fi
        done
    fi

    export INFLUXD_CONFIG_PATH="${config_path}"
}

function influxd::config::get()
{
  # The configuration is a mixture of both configuration files, environment
  # variables, and command line options. Consequentially, this prevents the
  # configuration from being known *before* executing influxd. This
  # emulates what `influx server-config` would return.
  declare -r COLUMN_ENVIRONMENT=0
  declare -r COLUMN_DEFAULT=1
  declare -rA table=(
    ##################################################################################
    # PRIMARY_KEY       # ENVIRONMENT VARIABLE      # DEFAULT                        #
    ##################################################################################
            [bolt-path]=" INFLUXD_BOLT_PATH         | /var/lib/influxdb2/influxd.bolt"
          [engine-path]=" INFLUXD_ENGINE_PATH       | /var/lib/influxdb2/engine"
            [log-level]=" INFLUXD_LOG_LEVEL         | info"
              [tls-key]=" INFLUXD_TLS_KEY           | "
             [tls-cert]=" INFLUXD_TLS_CERT          | "
    [http-bind-address]=" INFLUXD_HTTP_BIND_ADDRESS | :8086"
  )

  function table::get()
  {
    ( # don't leak shopt options
      local row
      local value

      shopt -s extglob
      # Unfortunately, bash doesn't support multidimensional arrays. This
      # retrieves the corresponding row from the array, splits the column
      # from row, and strips leading and trailing whitespace. `extglob`
      # is required for this to delete multiple spaces.
      IFS='|' row=(${table[${1}]})
      value=${row[${2}]}
      value="${value##+([[:space:]])}"
      value="${value%%+([[:space:]])}"
      echo "${value}"
    )
  }

  #
  # Parse Value from Arguments
  #

  local primary_key="${1}" && shift

  # Command line arguments take precedence over all other configuration
  # sources. This supports two argument formats and ignores unspecified
  # arguments even if they contain errors. These will be caught when
  # influxd is started.
  while [[ "${#}" -gt 0 ]] ; do
    case ${1} in
      --${primary_key}=*) echo "${1/#"--${primary_key}="}" && return ;;
      --${primary_key}* ) echo "${2}"                      && return ;;
      *) shift ;;
    esac
  done

  #
  # Parse Value from Environment
  #

  local value

  # If no command line arguments match, retrieve the corresponding environment
  # variable. This differentiates between unset and empty variables. If empty,
  # it is possible that variable was intentionally emptied; therefore, this
  # returns nothing when empty.
  value="$(table::get "${primary_key}" ${COLUMN_ENVIRONMENT})"
  if [[ ${!value+x} ]] ; then
    echo "${!value}" && return
  fi

  #
  # Parse Value from Configuration
  #
  dasel -f "${INFLUXD_CONFIG_PATH}" -s "${primary_key}" -w - 2>/dev/null || \
    table::get "${primary_key}" "${COLUMN_DEFAULT}"
}

function set_data_paths () {
    BOLT_PATH="$(influxd::config::get bolt-path "${@}")"
    ENGINE_PATH="$(influxd::config::get engine-path "${@}")"
    export BOLT_PATH ENGINE_PATH
}

# Ensure all the data directories needed by influxd exist with the right permissions.
function create_directories () {
    local -r bolt_dir="$(dirname "${BOLT_PATH}")"
    local user=$(id -u)

    mkdir -p "${bolt_dir}" "${ENGINE_PATH}"
    chmod 700 "${bolt_dir}" "${ENGINE_PATH}" || :

    mkdir -p "${CONFIG_VOLUME}" || :
    chmod 775 "${CONFIG_VOLUME}" || :

    if [ ${user} = 0 ]; then
        find "${bolt_dir}" \! -user influxdb -exec chown influxdb '{}' +
        find "${ENGINE_PATH}" \! -user influxdb -exec chown influxdb '{}' +
        find "${CONFIG_VOLUME}" \! -user influxdb -exec chown influxdb '{}' +
    fi
}

# Read password and username from file to avoid unsecure env variables
if [ -n "${DOCKER_INFLUXDB_INIT_PASSWORD_FILE}" ]; then [ -e "${DOCKER_INFLUXDB_INIT_PASSWORD_FILE}" ] && DOCKER_INFLUXDB_INIT_PASSWORD=$(cat "${DOCKER_INFLUXDB_INIT_PASSWORD_FILE}") || echo "DOCKER_INFLUXDB_INIT_PASSWORD_FILE defined, but file not existing, skipping."; fi
if [ -n "${DOCKER_INFLUXDB_INIT_USERNAME_FILE}" ]; then [ -e "${DOCKER_INFLUXDB_INIT_USERNAME_FILE}" ] && DOCKER_INFLUXDB_INIT_USERNAME=$(cat "${DOCKER_INFLUXDB_INIT_USERNAME_FILE}") || echo "DOCKER_INFLUXDB_INIT_USERNAME_FILE defined, but file not existing, skipping."; fi
if [ -n "${DOCKER_INFLUXDB_INIT_ADMIN_TOKEN_FILE}" ]; then [ -e "${DOCKER_INFLUXDB_INIT_ADMIN_TOKEN_FILE}" ] && DOCKER_INFLUXDB_INIT_ADMIN_TOKEN=$(cat "${DOCKER_INFLUXDB_INIT_ADMIN_TOKEN_FILE}") || echo "DOCKER_INFLUXDB_INIT_ADMIN_TOKEN_FILE defined, but file not existing, skipping."; fi

# List of env vars required to auto-run setup or upgrade processes.
declare -ra REQUIRED_INIT_VARS=(
  DOCKER_INFLUXDB_INIT_USERNAME
  DOCKER_INFLUXDB_INIT_PASSWORD
  DOCKER_INFLUXDB_INIT_ORG
  DOCKER_INFLUXDB_INIT_BUCKET
)

# Ensure all env vars required to run influx setup or influxd upgrade are set in the env.
function ensure_init_vars_set () {
    local missing_some=0
    for var in "${REQUIRED_INIT_VARS[@]}"; do
        if [ -z "${!var}" ]; then
            log error "missing parameter, cannot init InfluxDB" parameter ${var}
            missing_some=1
        fi
    done
    if [ ${missing_some} = 1 ]; then
        exit 1
    fi
}

# If exiting on error, delete all bolt and engine files.
# If we didn't do this, the container would see the boltdb file on reboot and assume
# the DB is already full set up.
function cleanup_influxd () {
    log warn "cleaning bolt and engine files to prevent conflicts on retry" bolt_path "${BOLT_PATH}" engine_path "${ENGINE_PATH}"
    rm -rf "${BOLT_PATH}" "${ENGINE_PATH}/"*
}

# Ping influxd until it responds or crashes.
# Used to block execution until the server is ready to process setup requests.
function wait_for_influxd () {
    local -r influxd_pid=$1
    local ping_count=0
    while kill -0 "${influxd_pid}" && [ ${ping_count} -lt ${INFLUXD_INIT_PING_ATTEMPTS} ]; do
        sleep 1
        log info "pinging influxd..." ping_attempt ${ping_count}
        ping_count=$((ping_count+1))
        if influx ping &> /dev/null; then
            log info "got response from influxd, proceeding" total_pings ${ping_count}
            return
        fi
    done
    if [ ${ping_count} -eq ${INFLUXD_INIT_PING_ATTEMPTS} ]; then
        log error "influxd took too long to start up" total_pings ${ping_count}
    else
        log error "influxd crashed during startup" total_pings ${ping_count}
    fi
    exit 1
}

# Create an initial user/org/bucket in the DB using the influx CLI.
function setup_influxd () {
    local -a setup_args=(
        --force
        --username "${DOCKER_INFLUXDB_INIT_USERNAME}"
        --password "${DOCKER_INFLUXDB_INIT_PASSWORD}"
        --org "${DOCKER_INFLUXDB_INIT_ORG}"
        --bucket "${DOCKER_INFLUXDB_INIT_BUCKET}"
        --name "${DOCKER_INFLUXDB_INIT_CLI_CONFIG_NAME}"
    )
    if [ -n "${DOCKER_INFLUXDB_INIT_RETENTION}" ]; then
        setup_args=("${setup_args[@]}" --retention "${DOCKER_INFLUXDB_INIT_RETENTION}")
    fi
    if [ -n "${DOCKER_INFLUXDB_INIT_ADMIN_TOKEN}" ]; then
        setup_args=("${setup_args[@]}" --token "${DOCKER_INFLUXDB_INIT_ADMIN_TOKEN}")
    fi

    influx setup "${setup_args[@]}"
}

# Get the IDs of the initial user/org/bucket created during setup, and export them into the env.
# We do this to help with arbitrary user scripts, since many influx CLI commands only take IDs.
function set_init_resource_ids () {
    DOCKER_INFLUXDB_INIT_USER_ID="$(influx user list -n "${DOCKER_INFLUXDB_INIT_USERNAME}" --hide-headers | cut -f 1)"
    DOCKER_INFLUXDB_INIT_ORG_ID="$(influx org list -n "${DOCKER_INFLUXDB_INIT_ORG}" --hide-headers | cut -f 1)"
    DOCKER_INFLUXDB_INIT_BUCKET_ID="$(influx bucket list -n "${DOCKER_INFLUXDB_INIT_BUCKET}" --hide-headers | cut -f 1)"
    export DOCKER_INFLUXDB_INIT_USER_ID DOCKER_INFLUXDB_INIT_ORG_ID DOCKER_INFLUXDB_INIT_BUCKET_ID
}

# Allow users to mount arbitrary startup scripts into the container,
# for execution after initial setup/upgrade.
declare -r USER_SCRIPT_DIR=/docker-entrypoint-initdb.d

# Check if user-defined setup scripts have been mounted into the container.
function user_scripts_present () {
    if [ ! -d ${USER_SCRIPT_DIR} ]; then
        return 1
    fi
    test -n "$(find ${USER_SCRIPT_DIR} -name "*.sh" -type f -executable)"
}

# Execute all shell files mounted into the expected path for user-defined startup scripts.
function run_user_scripts () {
    if [ -d ${USER_SCRIPT_DIR} ]; then
        log info "Executing user-provided scripts" script_dir ${USER_SCRIPT_DIR}
        run-parts --regex ".*sh$" --report --exit-on-error ${USER_SCRIPT_DIR}
    fi
}

# Helper used to propagate signals received during initialization to the influxd
# process running in the background.
function handle_signal () {
    kill -${1} ${2}
    wait ${2}
}

# Perform initial setup on the InfluxDB instance, either by setting up fresh metadata
# or by upgrading existing V1 data.
function init_influxd () {
    if [[ "${DOCKER_INFLUXDB_INIT_MODE}" != setup ]]; then
        log error "found invalid DOCKER_INFLUXDB_INIT_MODE, only 'setup' is supported" DOCKER_INFLUXDB_INIT_MODE "${DOCKER_INFLUXDB_INIT_MODE}"
        exit 1
    fi
    ensure_init_vars_set
    trap "cleanup_influxd" EXIT

    local -r final_bind_addr="$(influxd::config::get http-bind-address "${@}")"
    local -r init_bind_addr=":${INFLUXD_INIT_PORT}"
    if [ "${init_bind_addr}" = "${final_bind_addr}" ]; then
      log warn "influxd setup binding to same addr as final config, server will be exposed before ready" addr "${init_bind_addr}"
    fi
    local final_host_scheme="http"
    if [ -n "$(influxd::config::get tls-cert "${@}")" ] &&
       [ -n "$(influxd::config::get tls-key  "${@}")" ]
    then
      final_host_scheme="https"
    fi

    case ${INFLUXD_CONFIG_PATH,,} in
      *.toml)       local influxd_config_format=toml ;;
      *.json)       local influxd_config_format=json ;;
      *.yaml|*.yml) local influxd_config_format=yaml ;;
    esac

    # Generate a config file with a known HTTP port, and TLS disabled.
    local -r init_config=/tmp/config.json
    (
      dasel -r "${influxd_config_format}" -w json \
        | dasel -r json put http-bind-address -v "${init_bind_addr}" \
        `# insert "tls-cert" and "tls-key" so delete succeeds` \
        | dasel -r json put tls-cert -v ''                     \
        | dasel -r json put tls-key  -v ''                     \
        `# delete "tls-cert" and "tls-key"` \
        | dasel -r json delete tls-cert     \
        | dasel -r json delete tls-key
    ) <"${INFLUXD_CONFIG_PATH}" | tee "${init_config}"

    # Start influxd in the background.
    log info "booting influxd server in the background"
    INFLUXD_CONFIG_PATH="${init_config}" INFLUXD_HTTP_BIND_ADDRESS="${init_bind_addr}" INFLUXD_TLS_CERT='' INFLUXD_TLS_KEY='' /usr/local/bin/influxd &
    local -r influxd_init_pid="$!"
    trap "handle_signal TERM ${influxd_init_pid}" TERM
    trap "handle_signal INT ${influxd_init_pid}" INT

    export INFLUX_HOST="http://localhost:${INFLUXD_INIT_PORT}"
    wait_for_influxd "${influxd_init_pid}"

    # Use the influx CLI to create an initial user/org/bucket.
    setup_influxd

    set_init_resource_ids
    run_user_scripts

    log info "initialization complete, shutting down background influxd"
    kill -TERM "${influxd_init_pid}"
    wait "${influxd_init_pid}" || true
    trap - EXIT INT TERM

    # Rewrite the CLI configs to point at the server's final HTTP address.
    local -r final_port="$(echo "${final_bind_addr}" | sed -E 's#[^:]*:(.*)#\1#')"
    sed -i "s#http://localhost:${INFLUXD_INIT_PORT}#${final_host_scheme}://localhost:${final_port}#g" "${INFLUX_CONFIGS_PATH}"
}

# Check if the --help or -h flag is set in a list of CLI args.
function check_help_flag () {
  for arg in "${@}"; do
      if [ "${arg}" = --help ] || [ "${arg}" = -h ]; then
          return 0
      fi
  done
  return 1
}

function main () {
    # Ensure INFLUXD_CONFIG_PATH is set.
    set_config_path

    local run_influxd=false
    if [[ $# = 0 || "$1" = run || "${1:0:1}" = '-' ]]; then
        run_influxd=true
    elif [[ "$1" = influxd && ($# = 1 || "$2" = run || "${2:0:1}" = '-') ]]; then
        run_influxd=true
        shift 1
    fi

    if ! ${run_influxd}; then
      exec "${@}"
    fi

    if [ "$1" = run ]; then
        shift 1
    fi

    if ! check_help_flag "${@}"; then
        # Configure logging for our wrapper.
        set_global_log_level "${@}"
        # Configure data paths used across functions.
        set_data_paths "${@}"
        # Ensure volume directories exist w/ correct permissions.
        create_directories
    fi

    if [ -f "${BOLT_PATH}" ]; then
        log info "found existing boltdb file, skipping setup wrapper" bolt_path "${BOLT_PATH}"
    elif [ -z "${DOCKER_INFLUXDB_INIT_MODE}" ]; then
        log warn "boltdb not found at configured path, but DOCKER_INFLUXDB_INIT_MODE not specified, skipping setup wrapper" bolt_path "${BOLT_PATH}"
    else
        init_influxd "${@}"
        # Set correct permission on volume directories again.
        create_directories
    fi

    if [ "$(id -u)" = 0 ]; then
        exec gosu influxdb "$BASH_SOURCE" "${@}"
    fi

    # Run influxd.
    exec /usr/local/bin/influxd "${@}"
}

main "${@}"
