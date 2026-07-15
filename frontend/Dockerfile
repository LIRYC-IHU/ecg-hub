# Stage 1: Build React SPA
FROM node:20-alpine AS builder

WORKDIR /app

# Install yarn (already present on node:20-alpine as corepack, enable it)
RUN corepack enable && corepack prepare yarn@1.22.22 --activate

COPY package.json yarn.lock ./
RUN yarn --frozen-lockfile

COPY . .
RUN yarn build

# Stage 2: Serve with Nginx
#
# No USER directive by design: the nginx master must start as root to bind :80
# (prod runs network_mode: host behind Traefik, and no-new-privileges blocks a
# setcap workaround) and then drops its workers to the unprivileged `nginx`
# user. The container is hardened in docker-compose.yml instead: cap_drop ALL
# with only NET_BIND_SERVICE/CHOWN/SETUID/SETGID/DAC_OVERRIDE added back, plus
# no-new-privileges:true.
FROM nginx:alpine

COPY --from=builder /app/dist /usr/share/nginx/html

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]
