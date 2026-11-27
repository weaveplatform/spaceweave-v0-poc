FROM golang:1.27.4-bookworm AS build

WORKDIR /src
ENV GOWORK=off CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY api/ api/
COPY pkg/ pkg/
RUN go build -trimpath -o /out/spaceweave ./cmd/spaceweave \
    && go build -trimpath -o /out/spaceweavectl ./cmd/spaceweavectl

FROM node:22-bookworm-slim

WORKDIR /app
COPY --from=build /out/spaceweave /usr/local/bin/spaceweave
COPY --from=build /out/spaceweavectl /usr/local/bin/spaceweavectl
COPY web/floorplan-iso/ web/floorplan-iso/
RUN mkdir -p floorplans/inbox web/floorplan-iso/generated \
    && chown -R node:node /app
USER node
EXPOSE 8000
ENTRYPOINT ["spaceweave"]
CMD ["serve", "--addr", "0.0.0.0:8000"]
