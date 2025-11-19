import DownloadButton from "./DownloadButton";
import { AppleIcon, WindowsIcon, LinuxIcon, Downloads } from "./Downloads";
import { DetectedOS } from "../utils/detectOS";

interface HeroProps {
  title: string;
  description: string;
  client: DetectedOS | undefined;
  downloads: Downloads;
}

export default function Hero({ title, description, client, downloads }: HeroProps) {
  if (!client?.platform) {
    return null;
  }

  const clientPlatform = client.platform as keyof Downloads;
  const platformValue = downloads[clientPlatform];
  const primaryLink =
    typeof platformValue === "string"
      ? platformValue
      : "primary" in (platformValue as any)
      ? (platformValue as any).primary
      : (platformValue as any);
  const secondaryLinks =
    typeof platformValue !== "string" && "secondary" in (platformValue as any)
      ? (platformValue as any).secondary
      : undefined;

  const iconForPlatform =
    clientPlatform === "macos"
      ? <AppleIcon />
      : clientPlatform === "windows"
      ? <WindowsIcon />
      : <LinuxIcon />;

  return (
    <section className="relative overflow-hidden px-4 py-16 sm:px-6 sm:py-24 lg:px-8 lg:py16">
      <div className="relative mx-auto max-w-7xl flex px-5 py-24 md:flex-row flex-col items-center">
        <div className="lg:flex-grow md:w-1/2 lg:pr-24 md:pr-16 flex flex-col md:items-start md:text-left mb-16 md:mb-0 items-center text-center animate-fade-in">
          <h1 className="text-4xl font-bold tracking-tight sm:text-4xl text-3xl mb-4 font-medium text-gray-900">
            {title}
          </h1>
          <p className="mb-8 leading-relaxed">{description}</p>
          <div className="flex justify-center">
            <DownloadButton
              primaryLink={primaryLink}
              secondaryLinks={secondaryLinks}
              icon={iconForPlatform}
            />
          </div>
        </div>
        <div className="absolute inset-0 -z-10 overflow-hidden" aria-hidden="true">
          <div className="absolute left-1/2 top-0 -translate-x-1/2 blur-3xl opacity-30">
            <div className="aspect-[1155/678] w-[72.1875rem] bg-gradient-to-tr from-primary-400 to-primary-600" />
          </div>
        </div>
        <div className="lg:max-w-lg lg:w-full md:w-1/2 w-5/6">
          <img className="object-cover object-center rounded" alt="hero" src="/citeck-launcher/screenshots/screenshot_main.png" />
        </div>
      </div>
    </section>
  );
}
