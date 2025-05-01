# Этап 1: Сборка
FROM alpine:3.20 AS builder

# Устанавливаем зависимости для сборки
RUN apk add --no-cache \
    bash git make curl build-base protoc nodejs npm coreutils && \
    npm install -g yarn

# Устанавливаем Go
ENV GO_VERSION=1.21.13
RUN curl -LO https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz && \
    tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz && \
    rm go${GO_VERSION}.linux-amd64.tar.gz
ENV PATH=$PATH:/usr/local/go/bin
ENV GOPATH=/go
ENV PATH=$PATH:$GOPATH/bin

# Устанавливаем Rust
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
ENV PATH=$PATH:/root/.cargo/bin

# Копируем исходники и собираем
WORKDIR /app
COPY influxdb/ /app
RUN make clean && make

# Этап 2: Финальный образ
FROM alpine:3.20

# Устанавливаем минимальные зависимости
RUN apk add --no-cache \
    bash ca-certificates curl gnupg run-parts su-exec tzdata && \
    update-ca-certificates && \
    echo 'hosts: files dns' >> /etc/nsswitch.conf

# Устанавливаем dasel для парсинга конфигурации
RUN case "$(apk --print-arch)" in \
      x86_64)  arch=amd64 ;; \
      aarch64) arch=arm64 ;; \
      *) echo 'Unsupported architecture' && exit 1 ;; \
    esac && \
    curl -fL "https://github.com/TomWright/dasel/releases/download/v2.4.1/dasel_linux_${arch}.gz" | gzip -d > /usr/local/bin/dasel && \
    case ${arch} in \
      amd64) echo '8e9fb0aa24e35774fab792005f05f9df141c22ec0a7436c7329a932582a10200  /usr/local/bin/dasel' ;; \
      arm64) echo '535f0f4c6362aa4b773664f7cfdb52d86f2723eac52a1aca6dfc6a69e2341c17  /usr/local/bin/dasel' ;; \
    esac | sha256sum -c - && \
    chmod +x /usr/local/bin/dasel

# Создаём пользователя и группу
RUN addgroup -S -g 1000 influxdb && \
    adduser -S -G influxdb -u 1000 -h /home/influxdb -s /bin/sh influxdb && \
    mkdir -p /home/influxdb && \
    chown -R influxdb:influxdb /home/influxdb

# Копируем бинарники
COPY --from=builder /app/bin/linux/influxd /usr/local/bin/influxd
COPY --from=builder /app/bin/linux/influx /usr/local/bin/influx

# Копируем entrypoint.sh
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Копируем конфигурацию по умолчанию
COPY default-config.yml /etc/defaults/influxdb2/config.yml
RUN chown influxdb:influxdb /etc/defaults/influxdb2/config.yml

# Создаём директории
RUN mkdir /docker-entrypoint-initdb.d && \
    mkdir -p /var/lib/influxdb2 && chown -R influxdb:influxdb /var/lib/influxdb2 && \
    mkdir -p /etc/influxdb2 && chown -R influxdb:influxdb /etc/influxdb2

VOLUME /var/lib/influxdb2 /etc/influxdb2
EXPOSE 8086

# Настраиваем переменные окружения, как в официальном образе
ENV INFLUX_CONFIGS_PATH=/etc/influxdb2/influx-configs
ENV INFLUXD_INIT_PORT=9999
ENV INFLUXD_INIT_PING_ATTEMPTS=600
ENV DOCKER_INFLUXDB_INIT_CLI_CONFIG_NAME=default

USER influxdb
ENTRYPOINT ["/entrypoint.sh"]
CMD ["influxd"]
