import type { Metadata } from "next";
import { notFound } from "next/navigation";

import { LandingPage } from "@/components/landing/landing-page";
import { getAppName } from "@/lib/app-config";
import { LOCALES, t, type Locale } from "@/lib/i18n/messages";

export const dynamic = "force-static";
export const dynamicParams = false;
export const revalidate = 60;

export function generateStaticParams() {
  return LOCALES.map((locale) => ({ locale }));
}

function parseLandingLocale(value: string): Locale | null {
  return LOCALES.includes(value as Locale) ? (value as Locale) : null;
}

interface LocalizedLandingPageProps {
  params: Promise<{ locale: string }>;
}

export async function generateMetadata({
  params
}: LocalizedLandingPageProps): Promise<Metadata> {
  const { locale: rawLocale } = await params;
  const locale = parseLandingLocale(rawLocale);
  if (!locale) return {};

  return {
    title: getAppName(),
    description: t(locale, "landing.hero.lead"),
    alternates: {
      canonical: locale === "zh-CN" ? "/zh-CN" : "/en",
      languages: {
        "zh-CN": "/zh-CN",
        en: "/en"
      }
    }
  };
}

export default async function LocalizedLandingPage({ params }: LocalizedLandingPageProps) {
  const { locale: rawLocale } = await params;
  const locale = parseLandingLocale(rawLocale);
  if (!locale) notFound();

  return <LandingPage locale={locale} />;
}
