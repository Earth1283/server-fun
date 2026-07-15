#!/usr/bin/env bash
# Set Java 21 as the default JDK (SDKMAN-managed), overriding Java 25.
set -euo pipefail

SDKMAN_JAVA_DIR="/usr/local/sdkman/candidates/java"
TARGET_VERSION="21.0.10-ms"

if [[ ! -d "${SDKMAN_JAVA_DIR}/${TARGET_VERSION}" ]]; then
    echo "Error: ${TARGET_VERSION} not found in ${SDKMAN_JAVA_DIR}" >&2
    echo "Installed versions:" >&2
    ls "${SDKMAN_JAVA_DIR}" >&2
    exit 1
fi

export JAVA_HOME="${SDKMAN_JAVA_DIR}/${TARGET_VERSION}"
export PATH="${JAVA_HOME}/bin:${PATH}"

echo "JAVA_HOME set to ${JAVA_HOME}"
java -version
