FROM oven/bun:1.4.0 AS build
WORKDIR /workspace
COPY package.json bun.lock biome.json tsconfig.json ./
COPY apps/web/package.json apps/web/package.json
COPY services/auth/package.json services/auth/package.json
RUN bun install --frozen-lockfile
COPY apps/web apps/web
RUN bun run --cwd apps/web build

FROM nginx:1.29-alpine
COPY apps/web/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /workspace/apps/web/dist /usr/share/nginx/html
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --retries=3 CMD wget -qO- http://127.0.0.1:8080/health/live || exit 1
