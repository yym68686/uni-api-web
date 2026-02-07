import type { Metadata } from "next";
import { notFound } from "next/navigation";

import { DocsShell } from "@/components/docs/docs-shell";
import { getDocsAlternates, getDocsPage, parseDocsLocale } from "@/content/docs/registry";

interface DocsSlugPageProps {
  params: Promise<{ locale: string; slug: string[] }>;
}

export async function generateMetadata({ params }: DocsSlugPageProps): Promise<Metadata> {
  const { locale: rawLocale, slug } = await params;
  const locale = parseDocsLocale(rawLocale);
  const page = locale ? getDocsPage(locale, slug) : null;
  const alternates = getDocsAlternates(slug);

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

export default async function DocsSlugPage({ params }: DocsSlugPageProps) {
  const { locale: rawLocale, slug } = await params;
  const locale = parseDocsLocale(rawLocale);
  if (!locale) notFound();

  const page = getDocsPage(locale, slug);
  if (!page) notFound();
  return <DocsShell locale={locale} page={page} />;
}
