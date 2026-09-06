FROM oven/bun:1.4.0
WORKDIR /workspace
COPY package.json bun.lock ./
COPY apps/web/package.json apps/web/package.json
COPY services/auth/package.json services/auth/package.json
RUN bun install --frozen-lockfile --production
COPY services/auth services/auth
USER bun
EXPOSE 3001
CMD ["bun", "run", "--cwd", "services/auth", "start"]
