# syntax=docker/dockerfile:1
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum* ./
COPY internal ./internal
COPY cmd ./cmd
COPY proto ./proto
RUN CGO_ENABLED=0 go build -o /out/cloudinit ./cmd/cloudinit
RUN CGO_ENABLED=0 go build -o /out/cloudctl ./cmd/cloudctl
RUN CGO_ENABLED=0 go build -o /out/cloudnode ./cmd/cloudnode

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=build /out/cloudinit /app/cloudinit
COPY --from=build /out/cloudctl /app/cloudctl
COPY --from=build /out/cloudnode /app/cloudnode
ENV ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/app/cloudinit"]
