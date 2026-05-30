import { LandingPage } from "@/components/landing/landing-page";

export const dynamic = "force-static";
export const revalidate = 60;

export default function RootLandingPage() {
  return <LandingPage locale="en" />;
}
