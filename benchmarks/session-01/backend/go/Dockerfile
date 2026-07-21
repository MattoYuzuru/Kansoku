ARG GO_IMAGE=golang@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2
FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY main.go .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/kansoku-spike main.go

FROM scratch
COPY --from=build /out/kansoku-spike /kansoku-spike
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/kansoku-spike"]
