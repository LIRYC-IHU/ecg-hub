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
FROM nginx:alpine

COPY --from=builder /app/dist /usr/share/nginx/html

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]
