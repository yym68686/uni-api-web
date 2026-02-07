import type { Metadata } from "next";
import { notFound } from "next/navigation";

import { DocsShell } from "@/components/docs/docs-shell";
import { getDocsAlternates, getDocsPage, parseDocsLocale } from "@/content/docs/registry";

interface DocsLocalePageProps {
  params: Promise<{ locale: string }>;
}

export async function generateMetadata({ params }: DocsLocalePageProps): Promise<Metadata> {
  const { locale: rawLocale } = await params;
  const locale = parseDocsLocale(rawLocale);
  const page = locale ? getDocsPage(locale, []) : null;
  const alternates = getDocsAlternates([]);

  const canonical = locale === "zh-CN" ? alternates["zh-CN"] : alternates.en;

  return {
    title: page?.title ?? undefined,
    description: page?.description ?? undefined,
    alternates: {
      canonical,
      languages: alternates
    }
  };
}

export default async function DocsLocalePage({ params }: DocsLocalePageProps) {
  const { locale: rawLocale } = await params;
  const locale = parseDocsLocale(rawLocale);
  if (!locale) notFound();

  const page = getDocsPage(locale, []);
  if (!page) notFound();
  return <DocsShell locale={locale} page={page} />;
}
