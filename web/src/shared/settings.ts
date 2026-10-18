import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import type { AIProvider, Settings } from "../api/types";

/** The defaults before anything is saved; also used when the settings cannot be read. */
export const builtInSettings: Settings = {
  aiProvider: "",
  aiModels: {},
  aiEndpoints: {},
  sensitive: false,
  includeComments: true,
  viewerAdaptive: true,
  asrEngine: "",
  asrPaths: {},
  cutSearchRounds: 4,
  cutSearchKb: 80,
};

/** The saved defaults (the data folder's settings.env). */
export function useSettings() {
  return useQuery({ queryKey: ["settings"], queryFn: api.settings });
}

/** The model a provider starts with: the saved default, else the provider's own. */
export function startModel(settings: Settings | undefined, p: AIProvider | undefined): string {
  if (!p) return "";
  return settings?.aiModels[p.id] || p.defaultModel;
}
