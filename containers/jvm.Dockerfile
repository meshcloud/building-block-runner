# shared Dockerfile used for JVM runners
FROM --platform=$BUILDPLATFORM gradle:8.13.0-jdk21 AS builder

WORKDIR /workspace
COPY . .

ARG RUNNER_MODULE
RUN ./gradlew --no-daemon :${RUNNER_MODULE}:bootJar

FROM eclipse-temurin:21-jre

RUN useradd meshcloud --uid 2000 --user-group && \
  chmod 0777 /opt/java/openjdk/lib/security/cacerts

ARG VERSION=dev
ENV VERSION=${VERSION}
ENV CUSTOM_CA_CERTS_PATH=/certs
ENV PORT=8080
EXPOSE 8080

ARG RUNNER_MODULE
COPY --from=builder --chown=meshcloud:meshcloud --chmod=755 /workspace/${RUNNER_MODULE}/build/libs/${RUNNER_MODULE}.jar /app/executable
COPY --chown=meshcloud:meshcloud --chmod=755 containers/entrypoint-jvm.sh /app/entrypoint.sh

WORKDIR /app
USER 2000
ENTRYPOINT ["/app/entrypoint.sh", "java", "-jar", "/app/executable"]
