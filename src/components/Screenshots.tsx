interface ScreenshotsProps {
  images: string[];
  title?: string;
}

export default function Screenshots({ images, title = 'Screenshots' }: ScreenshotsProps) {
  if (images.length === 0) {
    return null;
  }

  return (
    <section className="px-4 py-12 sm:px-6 sm:py-16 lg:px-8 lg:py-24">
      <div className="mx-auto max-w-7xl">
        <h2 className="mb-8 text-center text-3xl font-bold text-gray-900 dark:text-white sm:mb-12 sm:text-4xl">
          {title}
        </h2>
        
        <div className="grid grid-cols-1 gap-6 sm:gap-8 md:grid-cols-2">
          {images.map((image, index) => (
            <div
              key={index}
              className="group relative overflow-hidden rounded-2xl bg-gray-100 shadow-lg transition-smooth hover:shadow-2xl dark:bg-gray-800"
            >
              <div className="aspect-video w-full">
                <img
                  src={`/citeck-launcher${image}`}
                  alt={`Screenshot ${index + 1}`}
                  className="h-full w-full object-contain transition-transform duration-500 group-hover:scale-105"
                  loading="lazy"
                />
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
