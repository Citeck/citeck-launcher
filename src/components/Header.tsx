import React from 'react';

export default function Header(): React.JSX.Element {
  return (
    <header className="w-full bg-white dark:bg-gray-900 border-b border-gray-100 dark:border-gray-800">
      <div className="mx-auto max-w-7xl px-4">
        <div className="flex items-center justify-between h-16">
          <div className="flex items-center gap-3">
            <div className="flex-shrink-0">
              <img src="https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg" alt="Citeck Logo" className="h-8" />
            </div>
          </div>

          <nav className="flex items-center gap-4">
            <a
              href="https://github.com/Citeck/citeck-launcher"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 px-3 py-2 rounded-md text-sm font-medium text-gray-700 hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-gray-800"
              aria-label="Citeck Launcher on GitHub"
            >
              <svg className="h-5 w-5" viewBox="0 0 24 24" fill="currentColor" xmlns="http://www.w3.org/2000/svg">
                <path d="M12 .5C5.73.5.75 5.48.75 11.76c0 4.93 3.2 9.1 7.64 10.57.56.1.76-.24.76-.53 0-.26-.01-.95-.01-1.86-3.11.68-3.77-1.5-3.77-1.5-.51-1.29-1.24-1.63-1.24-1.63-1.01-.69.08-.68.08-.68 1.12.08 1.71 1.15 1.71 1.15.99 1.7 2.6 1.21 3.24.93.1-.72.39-1.21.71-1.49-2.48-.28-5.09-1.24-5.09-5.53 0-1.22.44-2.22 1.16-3-.12-.28-.5-1.41.11-2.94 0 0 .95-.31 3.12 1.16.9-.25 1.86-.38 2.82-.38.96 0 1.92.13 2.82.38 2.17-1.48 3.12-1.16 3.12-1.16.61 1.53.23 2.66.11 2.94.72.78 1.16 1.78 1.16 3 0 4.29-2.61 5.25-5.1 5.53.4.35.75 1.04.75 2.1 0 1.52-.01 2.75-.01 3.12 0 .29.2.64.77.53C20.05 20.86 23.25 16.69 23.25 11.76 23.25 5.48 18.27.5 12 .5z" />
              </svg>
              <span className="hidden sm:inline">GitHub</span>
            </a>
          </nav>
        </div>
      </div>
    </header>
  );
}
