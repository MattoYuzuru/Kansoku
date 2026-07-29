FROM golang:1.26-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS build
WORKDIR /src
COPY . .
RUN diff -qr web/dist internal/webui/dist \
    || (echo "embedded dashboard is stale; run web/scripts/build-and-embed.sh" >&2; exit 1)
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -trimpath -ldflags="-s -w" -o /out/kansoku ./cmd/kansoku \
    && mkdir -p /out/kansoku-data /out/kansoku-backups \
    && chmod 0700 /out/kansoku-data /out/kansoku-backups

FROM postgres@sha256:9a8afca54e7861fd90fab5fdf4c42477a6b1cb7d293595148e674e0a3181de15 AS pgtools

FROM scratch
COPY --from=pgtools / /
COPY --from=build /out/kansoku /kansoku
COPY --chown=65532:65532 --from=build /out/kansoku-data /var/lib/kansoku
COPY --chown=65532:65532 --from=build /out/kansoku-backups /var/lib/kansoku-backups
COPY tests/fixtures/session-02/raw-canary-input.json /usr/share/kansoku/privacy-canary.json
USER 65532:65532
EXPOSE 43100 4317 4318
ENTRYPOINT ["/kansoku"]
