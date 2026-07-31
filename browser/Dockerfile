FROM mcr.microsoft.com/playwright:v1.55.0-jammy

# Browsers are already bundled at /ms-playwright; don't re-download.
ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        libnss3-tools \
        ca-certificates \
        xvfb \
        x11-utils \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY package.json ./
RUN npm install --no-audit --no-fund

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

COPY playwright-example.js /app/playwright-example.js

EXPOSE 9222

ENTRYPOINT ["/entrypoint.sh"]
