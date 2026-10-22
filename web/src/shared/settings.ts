import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import type { AIProvider, AITask, Settings } from "../api/types";

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
  cutConfirmSearches: false,
  backupDir: "",
  backupVideo: true,
  backupVideoHeight: 720,
  autoAI: false,
  autoAIMaxRequests: 60,
  autoAICostCapUsd: 3,
  autoTranscriptFix: true,
  autoTranscribe: true,
  liveAudio: true,
  aiTaskModels: {},
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

/** The model a task uses with a provider (as the server picks it): set for the task, else recommended for it, else the provider's. */
export function taskModel(settings: Settings | undefined, p: AIProvider | undefined, task: AITask): string {
  if (!p) return "";
  return settings?.aiTaskModels[p.id]?.[task] || p.taskModels[task] || startModel(settings, p);
}
