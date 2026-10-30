likone@Henocks-MBP bpmngo % docker compose up -d --build --wait
[+] Building 1.6s (35/35) FINISHED                                                                                      
 => [internal] load local bake definitions                                                                         0.0s
 => => reading from stdin 1.01kB                                                                                   0.0s
 => [frontend internal] load build definition from Dockerfile                                                      0.0s
 => => transferring dockerfile: 684B                                                                               0.0s
 => [bpmngo internal] load build definition from dockerfile                                                        0.0s
 => => transferring dockerfile: 959B                                                                               0.0s
 => [frontend internal] load metadata for docker.io/library/nginx:1.27-alpine                                      0.6s
 => [frontend internal] load metadata for docker.io/library/node:22-alpine                                         0.0s
 => [bpmngo] resolve image config for docker-image://docker.io/docker/dockerfile:1                                 0.6s
 => CACHED [bpmngo] docker-image://docker.io/docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9  0.0s
 => => resolve docker.io/docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe  0.0s
 => [frontend internal] load .dockerignore                                                                         0.0s
 => => transferring context: 73B                                                                                   0.0s
 => [frontend builder 1/6] FROM docker.io/library/node:22-alpine@sha256:b0f9289e763b2cefba5faf46cb05f1a3b640b0441  0.0s
 => => resolve docker.io/library/node:22-alpine@sha256:b0f9289e763b2cefba5faf46cb05f1a3b640b0441ae697f5a4f319fa3d  0.0s
 => [frontend stage-1 1/3] FROM docker.io/library/nginx:1.27-alpine@sha256:65645c7bb6a0661892a8b03b89d0743208a18d  0.0s
 => => resolve docker.io/library/nginx:1.27-alpine@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76f  0.0s
 => [frontend internal] load build context                                                                         0.0s
 => => transferring context: 3.05kB                                                                                0.0s
 => CACHED [frontend builder 2/6] WORKDIR /app                                                                     0.0s
 => CACHED [frontend builder 3/6] COPY package*.json ./                                                            0.0s
 => CACHED [frontend builder 4/6] RUN npm install                                                                  0.0s
 => CACHED [frontend builder 5/6] COPY . .                                                                         0.0s
 => CACHED [frontend builder 6/6] RUN npm run build                                                                0.0s
 => CACHED [frontend stage-1 2/3] COPY --from=builder /app/dist /usr/share/nginx/html                              0.0s
 => CACHED [frontend stage-1 3/3] COPY nginx.conf /etc/nginx/conf.d/default.conf                                   0.0s
 => [frontend] exporting to image                                                                                  0.1s
 => => exporting layers                                                                                            0.0s
 => => exporting manifest sha256:806fa84cbc07bf489bda2b91fb12cf37cc8dba33a43f389b859a2a45cee17bcb                  0.0s
 => => exporting config sha256:e17bcd0fa2462da59fd8e25405aa9e9e82a5f11253b297044f1de68982a3f823                    0.0s
 => => exporting attestation manifest sha256:5faa80d4d7a38ad5b1d1996421dc32c5447b19795efcf9c686c4bc9c6f7087cd      0.0s
 => => exporting manifest list sha256:9d2b5f25efb86eafb7a8a55a77a9f265ac8ff4a17171673124fc62c4031d6b87             0.0s
 => => naming to docker.io/library/goflow-frontend:latest                                                          0.0s
 => => unpacking to docker.io/library/goflow-frontend:latest                                                       0.0s
 => [bpmngo internal] load metadata for docker.io/library/alpine:latest                                            0.6s
 => [bpmngo internal] load metadata for docker.io/library/golang:1.26-alpine                                       0.6s
 => [frontend] resolving provenance for metadata file                                                              0.0s
 => [bpmngo internal] load .dockerignore                                                                           0.1s
 => => transferring context: 214B                                                                                  0.0s
 => [bpmngo builder 1/5] FROM docker.io/library/golang:1.26-alpine@sha256:a2012b4a1d1f9f7e9f44e0d39b805a901e856b1  0.0s
 => => resolve docker.io/library/golang:1.26-alpine@sha256:a2012b4a1d1f9f7e9f44e0d39b805a901e856b1dd80b4e6ee4338d  0.0s
 => [bpmngo internal] load build context                                                                           0.1s
 => => transferring context: 270.20kB                                                                              0.1s
 => [bpmngo stage-1 1/4] FROM docker.io/library/alpine:latest@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf5196  0.0s
 => => resolve docker.io/library/alpine:latest@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371e  0.0s
 => CACHED [bpmngo stage-1 2/4] RUN apk --no-cache add ca-certificates tzdata                                      0.0s
 => CACHED [bpmngo stage-1 3/4] WORKDIR /app                                                                       0.0s
 => CACHED [bpmngo builder 2/5] RUN apk add --no-cache git ca-certificates tzdata                                  0.0s
 => CACHED [bpmngo builder 3/5] WORKDIR /app                                                                       0.0s
 => CACHED [bpmngo builder 4/5] COPY . .                                                                           0.0s
 => CACHED [bpmngo builder 5/5] RUN --mount=type=cache,target=/root/.cache/go-build     CGO_ENABLED=0 GOOS=linux   0.0s
 => CACHED [bpmngo stage-1 4/4] COPY --from=builder /app/bpmngo .                                                  0.0s
 => [bpmngo] exporting to image                                                                                    0.0s
 => => exporting layers                                                                                            0.0s
 => => exporting manifest sha256:9d9dc7c5a2ae4e278f717434dca1a3376aa98250b7c05eab281b05a93b461922                  0.0s
 => => exporting config sha256:de31f614e08519556a3910bdb7ae570432ccf991f8a1acca0881b5fae6c4645e                    0.0s
 => => exporting attestation manifest sha256:65f9534f62e5d81b19a4d98def5f2226b662bdc2dfedc7d9631c421a2a137778      0.0s
 => => exporting manifest list sha256:4ca50216043168bc580bdf019be7eeaea4cb5614096e9427ab30c6caa093deb1             0.0s
 => => naming to docker.io/library/goflow-bpmngo:latest                                                            0.0s
 => => unpacking to docker.io/library/goflow-bpmngo:latest                                                         0.0s
 => [bpmngo] resolving provenance for metadata file                                                                0.0s
[+] up 4/4
 ✔ Image goflow-bpmngo       Built                                                                                  1.7s
 ✔ Image goflow-frontend     Built                                                                                  1.7s
 ✔ Container bpmngo-postgres Healthy                                                                               10.6s
 ✘ Container bpmngo-app      Error dependency bpmngo failed to start                                                0.6s
dependency failed to start: container bpmngo-app is unhealthy