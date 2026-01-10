export const UI_EVENTS = {
  adminChannelsCreated: "uai:admin:channels:created",
  adminAnnouncementsCreated: "uai:admin:announcements:created",
  adminModelsRefreshed: "uai:admin:models:refreshed",
  logsRefreshed: "uai:logs:refreshed"
} as const;

export type UiEventName = (typeof UI_EVENTS)[keyof typeof UI_EVENTS];

export function dispatchUiEvent<TDetail>(name: UiEventName, detail?: TDetail) {
  if (typeof window === "undefined") return;
  window.dispatchEvent(new CustomEvent<TDetail>(name, { detail }));
}
