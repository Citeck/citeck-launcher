# Citeck Launcher Website

Static website for Citeck Launcher downloads built with Vite, React, TypeScript, and Tailwind CSS.

## Features

- **Modern Stack**: Vite + React + TypeScript
- **Tailwind CSS**: Mobile-first responsive design
- **Dark Mode**: Light and dark theme support
- **Accessible**: Semantic HTML and ARIA attributes
- **Animated**: Smooth CSS transitions
- **Static Export**: Builds to static files for GitHub Pages

## Development

### Prerequisites

- Node.js 18+
- Yarn

### Install Dependencies

```bash
yarn install
```

### Start Development Server

```bash
yarn dev
```

The site will be available at `http://localhost:5173`

### Build for Production

```bash
yarn build
```

The built files will be in the `dist/` directory.

### Preview Production Build

```bash
yarn preview
```

## Adding Screenshots

To add screenshots to the website:

1. Place your screenshot images in `public/screenshots/`
2. Update the `screenshots` array in `src/App.tsx`:

```typescript
const screenshots: string[] = [
  '/screenshots/screenshot1.png',
  '/screenshots/screenshot2.png',
];
```

## Deployment

The website is automatically deployed to GitHub Pages via GitHub Actions when changes are pushed to the repository.

## Architecture

- **Mobile-First**: Styles start with mobile and scale up with breakpoints
- **Component-Based**: Reusable React components with TypeScript
- **Accessibility**: ARIA labels, semantic HTML, keyboard navigation
- **Performance**: Lazy loading images, optimized builds

## Project Structure

```
website/
├── public/          # Static assets
├── src/
│   ├── components/  # React components
│   ├── styles/      # Global styles
│   ├── types.ts     # TypeScript types
│   ├── App.tsx      # Main app component
│   └── main.tsx     # Entry point
├── index.html
├── package.json
├── tailwind.config.js
├── tsconfig.json
└── vite.config.ts
```
