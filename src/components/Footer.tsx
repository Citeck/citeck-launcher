export default function Footer() {
  const currentYear = new Date().getFullYear();

  return (
    <footer className="border-t border-gray-200 px-4 py-8 dark:border-gray-800 sm:px-6 lg:px-8">
      <div className="mx-auto max-w-7xl">
        <div className="flex flex-col items-center justify-between gap-4 sm:flex-row">
          <p className="text-sm text-gray-600 dark:text-gray-400">
            © 2013 - {currentYear} Citeck LLC. All Rights Reserved.
          </p>
          
          <div className="flex gap-6">
            <a
              href="https://citeck-ecos.readthedocs.io/ru/latest/index.html"
              target="_blank"
              rel="noopener noreferrer"
              className="text-sm text-gray-600 transition-smooth hover:text-primary-600 dark:text-gray-400 dark:hover:text-primary-400"
            >
              Documentation
            </a>
            <a
              href="https://github.com/Citeck/citeck-launcher"
              target="_blank"
              rel="noopener noreferrer"
              className="text-sm text-gray-600 transition-smooth hover:text-primary-600 dark:text-gray-400 dark:hover:text-primary-400"
            >
              GitHub
            </a>
            <a
              href="mailto:support@citeck.ru"
              className="text-sm text-gray-600 transition-smooth hover:text-primary-600 dark:text-gray-400 dark:hover:text-primary-400"
            >
              Support
            </a>
          </div>
        </div>
      </div>
    </footer>
  );
}
