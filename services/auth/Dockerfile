FROM oven/bun:1.4.0
WORKDIR /workspace
RUN apt-get update && apt-get install -y --no-install-recommends gosu && rm -rf /var/lib/apt/lists/*
COPY package.json bun.lock ./
COPY apps/web/package.json apps/web/package.json
COPY services/auth/package.json services/auth/package.json
RUN bun install --frozen-lockfile --production
COPY services/auth services/auth
COPY --chmod=755 services/auth/docker-entrypoint.sh /usr/local/bin/mailflow-auth-entrypoint
EXPOSE 3001
ENTRYPOINT ["/usr/local/bin/mailflow-auth-entrypoint"]
