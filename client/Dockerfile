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
FROM nginx:1.27-alpine
COPY --from=build /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
