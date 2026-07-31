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

# Chrome runs as the non-root 'pwuser' (uid 1000) so goper's iptables
# owner-skip rule (uid 0) does not exempt its traffic.
RUN mkdir -p /home/pwuser/.config/goper-chrome /home/pwuser/.pki/nssdb \
    && chown -R pwuser:pwuser /home/pwuser

# Chrome sandbox: ensure the SUID helper is setuid root so the non-root
# pwuser can run Chrome with its sandbox enabled (no --no-sandbox).
RUN find /ms-playwright -name chrome-sandbox -exec chown root:root {} + \
    && find /ms-playwright -name chrome-sandbox -exec chmod 4755 {} +

# Disable Chrome's translate offer via managed policy (overrides the profile
# pref). Chromium reads /etc/chromium/policies/managed/ on Linux.
RUN mkdir -p /etc/chromium/policies/managed \
    && printf '{"TranslateEnabled": false}\n' > /etc/chromium/policies/managed/translate.json

WORKDIR /app

COPY package.json ./
RUN npm install --no-audit --no-fund

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

COPY playwright-example.js /app/playwright-example.js
COPY translate-probe.js /app/translate-probe.js

EXPOSE 9222

ENTRYPOINT ["/entrypoint.sh"]
