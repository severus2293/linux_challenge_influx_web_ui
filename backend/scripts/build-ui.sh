#!/usr/bin/env bash

set -e

# Определяем директории
SCRIPT_DIR=$(cd "$(dirname "${0}")" >/dev/null 2>&1 && pwd)
ROOT_DIR=$(dirname "${SCRIPT_DIR}")
STATIC_DIR="${ROOT_DIR}/static"
FRONTEND_DIR="${ROOT_DIR}/../frontend"

# Проверяем, что папка frontend существует и содержит package.json
if [ ! -d "${FRONTEND_DIR}" ] || [ ! -f "${FRONTEND_DIR}/package.json" ]; then
    echo "Frontend directory ${FRONTEND_DIR} not found or missing package.json. Please clone influxdata/ui into frontend."
    exit 1
fi

# Переходим в директорию фронтенда
cd "${FRONTEND_DIR}"

# Устанавливаем зависимости
echo "Installing frontend dependencies..."
yarn install

# Собираем UI
echo "Building frontend..."
yarn build

# Копируем собранные ассеты в static/data
echo "Copying built assets to ${STATIC_DIR}/data..."
mkdir -p "${STATIC_DIR}/data"
rm -rf "${STATIC_DIR}/data/build"
cp -r build "${STATIC_DIR}/data"

echo "UI assets successfully built and copied to ${STATIC_DIR}/data/build"