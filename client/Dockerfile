# Production image for the CodeArena frontend.
#
# The API origin is baked in at build time, since Vite inlines
# import.meta.env at compile time rather than reading it at runtime.
FROM node:22-alpine AS build
WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci

COPY . .
ARG VITE_API_URL
ENV VITE_API_URL=$VITE_API_URL
RUN npm run build

# nginx serves the built assets and proxies /api to the API container, so
# the browser sees one origin and no CORS preflight is needed.
#
# 1.29 rather than 1.27: this is the only container the internet can
# reach, and 1.27-alpine still sits on Alpine 3.21, whose support ends
# 2026-11-01. 1.29-alpine is built on Alpine 3.23 (supported until
# 2027-11-01). Same maintenance rule as the server image — the branch tag
# collects patches automatically, moving branches is a deliberate commit.
FROM nginx:1.29-alpine
COPY --from=build /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
