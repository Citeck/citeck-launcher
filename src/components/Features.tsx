interface Feature {
  title: string;
  description: string;
  icon: React.ReactNode;
}

interface FeaturesProps {
  features: Feature[];
}

export default function Features({ features }: FeaturesProps) {
  return (
    <section className="px-4 py-12 sm:px-6 sm:py-16 lg:px-8 lg:py-12">
      <div className="mx-auto max-w-7xl">
        <div className="grid grid-cols-1 gap-8 sm:grid-cols-2 lg:grid-cols-3 lg:gap-12">
          {features.map((feature, index) => (
            <div
              key={index}
              className="group relative animate-slide-up rounded-2xl bg-white p-6 shadow-sm ring-1 ring-gray-200 transition-smooth hover:shadow-md hover:ring-primary-300 dark:bg-gray-800 dark:ring-gray-700 dark:hover:ring-primary-600 sm:p-8"
              style={{ animationDelay: `${index * 100}ms` }}
            >
              <div className="mb-4 inline-flex h-12 w-12 items-center justify-center rounded-xl bg-primary-100 text-primary-600 dark:bg-primary-900/30 dark:text-primary-400 sm:h-14 sm:w-14">
                {feature.icon}
              </div>
              <h3 className="mb-3 text-lg font-semibold text-gray-900 dark:text-white sm:text-xl">
                {feature.title}
              </h3>
              <p className="text-sm text-gray-600 dark:text-gray-300 sm:text-base">
                {feature.description}
              </p>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
