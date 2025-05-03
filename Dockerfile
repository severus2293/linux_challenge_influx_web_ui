# Сборка фронтенда
FROM node:20 AS frontend-builder

WORKDIR /frontend
COPY frontend/ .
RUN yarn install && yarn build

# Базовый образ с Go для сборки бэкенда
FROM golang:1.23 AS backend-builder

# Устанавливаем зависимости для protoc, make и Rust
RUN apt-get update && apt-get install -y \
    protobuf-compiler \
    make \
    curl \
    && rm -rf /var/lib/apt/lists/* \
    && curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y \
    && . $HOME/.cargo/env \
    && cargo --version

# Устанавливаем рабочую директорию
WORKDIR /go/src/backend

# Копируем файлы go.mod и go.sum для кэширования зависимостей
COPY backend/go.mod backend/go.sum ./

# Загружаем зависимости
RUN go mod download

# Копируем остальные исходники бэкенда
COPY backend/ .

# Копируем собранные UI-ассеты из frontend-builder
COPY --from=frontend-builder /frontend/build ./static/data/build

# Собираем influxd
RUN . $HOME/.cargo/env && make build \
    && ls -l bin/linux

# Финальный образ на основе минимального дистрибутива
FROM debian:bookworm-slim

# Устанавливаем зависимости
RUN export DEBIAN_FRONTEND=noninteractive && \
    apt-get update -y && \
    apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      gnupg \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Устанавливаем dasel для парсинга конфигурации
RUN case "$(dpkg --print-architecture)" in \
      *amd64) arch=amd64 ;; \
      *arm64) arch=arm64 ;; \
      *) echo 'Unsupported architecture' && exit 1 ;; \
    esac && \
    curl -fL "https://github.com/TomWright/dasel/releases/download/v2.4.1/dasel_linux_${arch}.gz" | gzip -d > /usr/local/bin/dasel && \
    case ${arch} in \
      amd64) echo '8e9fb0aa24e35774fab792005f05f9df141c22ec0a7436c7329a932582a10200  /usr/local/bin/dasel' ;; \
      arm64) echo '535f0f4c6362aa4b773664f7cfdb52d86f2723eac52a1aca6dfc6a69e2341c17  /usr/local/bin/dasel' ;; \
    esac | sha256sum -c - && \
    chmod +x /usr/local/bin/dasel && \
    dasel --version

# Создаем пользователя и группу influxdb
RUN groupadd -r influxdb --gid=1000 && \
    useradd -r -g influxdb --uid=1000 --create-home --home-dir=/home/influxdb --shell=/bin/bash influxdb

# Устанавливаем gosu
ENV GOSU_VER=1.16
RUN case "$(dpkg --print-architecture)" in \
      *amd64) arch=amd64 ;; \
      *arm64) arch=arm64 ;; \
      *) echo 'Unsupported architecture' && exit 1 ;; \
    esac && \
    export GNUPGHOME="$(mktemp -d)" && \
    gpg --batch --keyserver keyserver.ubuntu.com --recv-keys \
      # Tianon Gravi <tianon@tianon.xyz> (gosu) \
      B42F6819007F00F88E364FD4036A9C25BF357DD4 && \
    curl -fLo /usr/local/bin/gosu "https://github.com/tianon/gosu/releases/download/$GOSU_VER/gosu-${arch}" \
         -fLo /usr/local/bin/gosu.asc "https://github.com/tianon/gosu/releases/download/$GOSU_VER/gosu-${arch}.asc" && \
    gpg --batch --verify /usr/local/bin/gosu.asc /usr/local/bin/gosu && \
    rm -rf /usr/local/bin/gosu.asc "$GNUPGHOME" && \
    chmod +x /usr/local/bin/gosu && \
    gosu --version && \
    gosu nobody true

# Копируем influxd из стадии backend-builder
COPY --from=backend-builder /go/src/backend/bin/linux/influxd /usr/local/bin/influxd

# Копируем собранный фронтенд
COPY --from=frontend-builder /frontend/build /usr/local/share/influxdb/static

# Устанавливаем influx CLI
ENV INFLUX_CLI_VERSION=2.7.5
RUN case "$(dpkg --print-architecture)" in \
      *amd64) arch=amd64 ;; \
      *arm64) arch=arm64 ;; \
      *) echo 'Unsupported architecture' && exit 1 ;; \
    esac && \
    export GNUPGHOME="$(mktemp -d)" && \
    gpg --batch --keyserver keyserver.ubuntu.com --recv-keys \
      # InfluxData Package Signing Key <support@influxdata.com> \
      9D539D90D3328DC7D6C8D3B9D8FF8E1F7DF8B07E && \
    curl -fLO "https://dl.influxdata.com/influxdb/releases/influxdb2-client-${INFLUX_CLI_VERSION}-linux-${arch}.tar.gz" \
         -fLO "https://dl.influxdata.com/influxdb/releases/influxdb2-client-${INFLUX_CLI_VERSION}-linux-${arch}.tar.gz.asc" && \
    gpg --batch --verify "influxdb2-client-${INFLUX_CLI_VERSION}-linux-${arch}.tar.gz.asc" \
                         "influxdb2-client-${INFLUX_CLI_VERSION}-linux-${arch}.tar.gz" && \
    tar xzf "influxdb2-client-${INFLUX_CLI_VERSION}-linux-${arch}.tar.gz" && \
    cp influx /usr/local/bin/influx && \
    rm -rf "influxdb2-client-${INFLUX_CLI_VERSION}-linux-${arch}" \
           "influxdb2-client-${INFLUX_CLI_VERSION}-linux-${arch}.tar.gz" \
           "influxdb2-client-${INFLUX_CLI_VERSION}-linux-${arch}.tar.gz.asc" "$GNUPGHOME" && \
    influx version

# Создаем директории
RUN mkdir -p /docker-entrypoint-initdb.d /var/lib/influxdb2 /etc/influxdb2 /etc/defaults/influxdb2 && \
    chown -R influxdb:influxdb /var/lib/influxdb2 /etc/influxdb2 /etc/defaults/influxdb2 /docker-entrypoint-initdb.d && \
    chmod 700 /var/lib/influxdb2 /etc/influxdb2 /etc/defaults/influxdb2 /docker-entrypoint-initdb.d

# Копируем конфигурацию по умолчанию
COPY default-config.yml /etc/defaults/influxdb2/config.yml
RUN chown influxdb:influxdb /etc/defaults/influxdb2/config.yml && \
    chmod 600 /etc/defaults/influxdb2/config.yml

# Копируем entrypoint-скрипт
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Указываем тома
VOLUME /var/lib/influxdb2 /etc/influxdb2 /docker-entrypoint-initdb.d

# Открываем порт
EXPOSE 8086

# Задаем переменные окружения
ENV INFLUX_CONFIGS_PATH=/etc/influxdb2/influx-configs
ENV INFLUXD_INIT_PORT=9999
ENV INFLUXD_INIT_PING_ATTEMPTS=600
ENV DOCKER_INFLUXDB_INIT_CLI_CONFIG_NAME=default

ENTRYPOINT ["/entrypoint.sh"]
CMD ["influxd"]
