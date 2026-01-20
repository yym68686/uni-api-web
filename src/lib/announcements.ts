import type { Locale } from "@/lib/i18n/messages";
import type { AnnouncementItem } from "@/lib/types";

function isZh(locale: Locale) {
  return locale === "zh-CN";
}

function pickByLocale(opts: {
  locale: Locale;
  zh?: string | null;
  en?: string | null;
  fallback?: string | null;
}) {
  const primary = isZh(opts.locale) ? opts.zh : opts.en;
  const secondary = isZh(opts.locale) ? opts.en : opts.zh;
  return primary ?? secondary ?? opts.fallback ?? "";
}

export function getAnnouncementTitle(announcement: AnnouncementItem, locale: Locale) {
  return pickByLocale({
    locale,
    zh: announcement.titleZh,
    en: announcement.titleEn,
    fallback: announcement.title
  });
}

export function getAnnouncementMeta(announcement: AnnouncementItem, locale: Locale) {
  return pickByLocale({
    locale,
    zh: announcement.metaZh,
    en: announcement.metaEn,
    fallback: announcement.meta
  });
}

