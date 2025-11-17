# Deployment Instructions

## Setting up GitHub Pages

To deploy the Citeck Launcher website to GitHub Pages, follow these steps:

### 1. Create the Pages Branch

First, create and push the `pages` branch if it doesn't exist:

```bash
git checkout -b pages
git push -u origin pages
```

### 2. Configure GitHub Pages Settings

1. Go to your repository on GitHub
2. Navigate to **Settings** → **Pages**
3. Under **Source**, select:
   - **Source**: GitHub Actions
4. Save the settings

### 3. Enable GitHub Actions

The GitHub Actions workflow is already configured in `.github/workflows/deploy-pages.yml`.

It will automatically trigger on:
- Push to the `pages` branch (changes in `website/` directory)
- Manual workflow dispatch

### 4. Push Your Changes

```bash
# Make sure you're on the pages branch
git checkout pages

# Add and commit your changes
git add .
git commit -m "Initial website setup"

# Push to trigger deployment
git push
```

### 5. Monitor Deployment

1. Go to the **Actions** tab in your repository
2. Watch the "Deploy to GitHub Pages" workflow run
3. Once completed, your site will be available at:
   ```
   https://[your-username].github.io/citeck-launcher/
   ```

## Updating the Website

To update the website:

1. Make changes in the `website/` directory
2. Commit and push to the `pages` branch:
   ```bash
   git checkout pages
   git add website/
   git commit -m "Update website"
   git push
   ```

The site will automatically rebuild and deploy.

## Updating Download Links

To update the download links when a new version is released:

1. Edit `website/src/App.tsx`
2. Update the version number and download URLs in the `downloads` object:
   ```typescript
   const downloads = {
     macos: {
       primary: {
         url: 'https://github.com/Citeck/citeck-launcher/releases/latest/download/citeck-launcher_X.X.X_macos_arm64.dmg',
         // ...
       },
       // ...
     },
     // ...
   };
   ```
3. Update the version in the `Downloads` component call:
   ```typescript
   <Downloads version="X.X.X" downloads={downloads} />
   ```
4. Commit and push the changes

## Troubleshooting

### Build Fails

If the build fails, check:
- Node.js version is 20 or higher
- All dependencies are correctly installed
- No TypeScript errors in the code

### Site Not Updating

If the site doesn't update after pushing:
- Check the Actions tab for workflow run status
- Verify the workflow completed successfully
- Clear your browser cache
- Wait a few minutes for GitHub's CDN to update

### 404 Error

If you get a 404 error:
- Verify GitHub Pages is enabled in repository settings
- Check that the workflow has run successfully
- Ensure the base path in `vite.config.ts` matches your repository name
