import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './tests',
  timeout: 60000,
  use: {
    baseURL: 'https://custom.launcher.ru:8443',
    ignoreHTTPSErrors: true,
    httpCredentials: {
      username: 'admin',
      password: 'admin',
    },
    launchOptions: {
      args: ['--ignore-certificate-errors'],
    },
  },
  projects: [
    {
      name: 'chromium',
      use: { browserName: 'chromium' },
    },
  ],
})
