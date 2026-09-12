FROM node:22-alpine
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm ci --omit=dev --no-audit --no-fund
COPY src ./src
# Non-root, like the promin container; /data/ytx holds the account store.
USER 65532:65532
ENV YTX_ADDR=:8091 YTX_DATA=/data/ytx
EXPOSE 8091
CMD ["node", "src/server.js"]
