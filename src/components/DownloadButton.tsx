import { useState, useRef, useEffect } from 'react';
import type { DownloadLink } from '../types';

interface DownloadButtonProps {
  primaryLink: DownloadLink;
  secondaryLinks?: DownloadLink[];
  icon: React.ReactNode;
  releaseName: String;
}

export default function DownloadButton({ primaryLink, secondaryLinks, icon, releaseName }: DownloadButtonProps) {
  const [isOpen, setIsOpen] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target as Node)) {
        setIsOpen(false);
      }
    };

    document.addEventListener('mousedown', handleClickOutside);

    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  const handleDownload = (link: DownloadLink) => {
    window.location.href = link.url;
    setIsOpen(false);
  };

  return (
    <div className="relative inline-block" ref={dropdownRef}>
      <div className="flex rounded-lg shadow-lg transition-smooth hover:shadow-xl">
        <button
          onClick={() => handleDownload(primaryLink)}
          className={`flex items-center gap-3 rounded-lg bg-primary-600 px-6 py-3 text-sm font-semibold text-white transition-smooth hover:bg-primary-700 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 sm:px-8 sm:py-4 sm:text-base${secondaryLinks && secondaryLinks.length > 0 ? ' rounded-r-none' : ''}`}
          aria-label={`Download for ${primaryLink.label}`}
        >
          <span className="h-5 w-5 sm:h-6 sm:w-6">{icon}</span>
          <span>Download for {primaryLink.label} ({releaseName})</span>
        </button>

        {secondaryLinks && secondaryLinks.length > 0 && (
          <button
            onClick={() => setIsOpen(!isOpen)}
            className="rounded-r-lg border-l border-primary-700 bg-primary-600 px-3 py-3 transition-smooth hover:bg-primary-700 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 sm:px-4 sm:py-4"
            aria-label="Show more download options"
            aria-expanded={isOpen}
            aria-haspopup="true"
          >
            <svg
              className={`h-5 w-5 text-white transition-transform duration-200 ${isOpen ? 'rotate-180' : ''}`}
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
              aria-hidden="true"
            >
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
            </svg>
          </button>
        )}
      </div>

      {secondaryLinks && secondaryLinks.length > 0 && isOpen && (
        <div className="absolute left-0 right-0 z-10 mt-2 animate-slide-down rounded-lg bg-white shadow-xl ring-1 ring-black ring-opacity-5 dark:bg-gray-800 dark:ring-gray-700">
          <div className="py-1" role="menu" aria-orientation="vertical">
            {secondaryLinks.map((link, index) => (
              <button
                key={index}
                onClick={() => handleDownload(link)}
                className="flex w-full items-center gap-3 px-4 py-3 text-left text-sm text-gray-700 transition-smooth hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-gray-700 sm:text-base"
                role="menuitem"
              >
                <span className="h-5 w-5 sm:h-6 sm:w-6">{icon}</span>
                <span>Download for {link.label}</span>
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
