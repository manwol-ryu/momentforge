import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { AIModel, AIProvider, Settings, SettingsInfo } from "../../api/types";
import { AIKeyField } from "../../shared/ai/AIKeyField";
import { queryState, useAppState } from "../../shared/appState";
import { RevealWords } from "../../shared/motion/RevealWords";
import { Segmented } from "../../shared/motion/Segmented";
import { Tabs } from "../../shared/motion/Tabs";
import { useDocumentTitle } from "../../shared/navigation";
import { useSettings } from "../../shared/settings";

type Section = "ai" | "collect";

/** Only the non-empty values of a map, in a stable order (empty means "the provider's own"). */
function filled(m: Record<string, string> | undefined): Record<string, string> {
  const out: Record<string, string> = {};
  for (const id of Object.keys(m ?? {}).sort()) {
    const v = m![id]!.trim();
    if (v) out[id] = v;
  }
  return out;
}

/** Only the values, with empty models and addresses left out. */
function valuesOf(s: Settings): Settings {
  return {
    aiProvider: s.aiProvider,
    aiModels: filled(s.aiModels),
    aiEndpoints: filled(s.aiEndpoints),
    sensitive: s.sensitive,
    includeComments: s.includeComments,
    viewerAdaptive: s.viewerAdaptive,
  };
}

/**
 * The settings screen: the AI provider and models the AI review starts
 * with, each provider's key (memory, or kept on this computer), and the
 * options the new-task forms start with. Defaults go to the data folder's
 * settings.env; keys never do.
 */
export function SettingsView() {
  const providers = useQuery({ queryKey: ["aiProviders"], queryFn: api.aiProviders });
  const settings = useSettings();
  useDocumentTitle("설정");
  useAppState(
    queryState({ isSuccess: providers.isSuccess && settings.isSuccess, isError: providers.isError || settings.isError }),
  );
  const failed = settings.error ?? providers.error;

  return (
    <div className="page-start">
      <header className="start-hero">
        <h1 className="display">
          <RevealWords text="설정" />
        </h1>
        <p className="page-lede">
          <RevealWords text="새 작업과 AI 검토가 시작할 기본값을 정합니다." start={1} />
        </p>
      </header>

      <div className="start-grid">
        <div className="start-main">
          <div className="frame">
            {settings.data && providers.data ? (
              <SettingsForm saved={settings.data} providers={providers.data.providers} />
            ) : failed ? (
              <p className="error settings-error" role="alert">
                {failed.message}
              </p>
            ) : (
              <p className="field-help settings-loading" aria-busy="true">
                설정을 불러오는 중
              </p>
            )}
          </div>
        </div>

        <aside className="start-side" aria-label="저장 위치와 키 안내">
          <section className="sheet" aria-labelledby="files-title">
            <h2 id="files-title" className="sheet-title">
              저장 위치
            </h2>
            <dl className="facts facts-stacked">
              <dt>기본값</dt>
              <dd>{settings.data?.file ?? "확인하는 중"}</dd>
              <dt>이 컴퓨터에 저장한 API 키</dt>
              <dd>{settings.data?.keyFile ?? "확인하는 중"}</dd>
            </dl>
            <p className="field-help">
              두 파일 모두 데이터 폴더 안에 있어 Git에 올라가지 않고, 나만 읽을 수 있는 권한으로 씁니다. 데이터 폴더를 바꾸면(--data)
              그 폴더의 설정을 씁니다.
            </p>
          </section>
          <section className="sheet" data-tint="peach" aria-labelledby="keys-title">
            <h2 id="keys-title" className="sheet-title">
              API 키
            </h2>
            <p className="field-help">
              키는 앱이 실행되는 동안 메모리에 둡니다. ‘이 컴퓨터에도 저장’을 고른 키만 .env 파일에 평문으로 남고, 지우면 파일에서도
              지웁니다. 키를 넣어도 AI로 보내기 전에는 늘 보낼 내용과 비용을 먼저 보여 드립니다.
            </p>
          </section>
        </aside>
      </div>
    </div>
  );
}

function SettingsForm({ saved, providers }: { saved: SettingsInfo; providers: AIProvider[] }) {
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<Settings>(() => valuesOf(saved));
  const [section, setSection] = useState<Section>("ai");
  const save = useMutation({
    mutationFn: () => api.saveSettings(valuesOf(draft)),
    onSuccess: (s) => {
      queryClient.setQueryData(["settings"], s);
      setDraft(valuesOf(s));
      // A saved address changes where a provider sends.
      void queryClient.invalidateQueries({ queryKey: ["aiProviders"] });
    },
  });
  const changed = JSON.stringify(valuesOf(draft)) !== JSON.stringify(valuesOf(saved));
  const change = (patch: Partial<Settings>) => {
    save.reset();
    setDraft((d) => ({ ...d, ...patch }));
  };
  const defaultProvider = providers.some((p) => p.id === draft.aiProvider) ? draft.aiProvider : (providers[0]?.id ?? "");
  // The choice above is also which provider's model, key and address are set below.
  const shown = providers.find((p) => p.id === defaultProvider);

  return (
    <div className="settings">
      <Tabs
        label="설정 종류"
        className="start-tabs"
        value={section}
        onChange={setSection}
        tabs={[
          {
            id: "ai",
            label: "AI 검토",
            content: (
              <div className="settings-ai">
                {providers.length > 1 && (
                  <Segmented
                    name="default-provider"
                    legend="기본 AI 제공자"
                    value={defaultProvider}
                    onChange={(id) => change({ aiProvider: id })}
                    options={providers.map((p) => ({ value: p.id, label: p.name }))}
                  />
                )}
                <p className="field-help">
                  작업 화면의 AI 검토 탭이 이 제공자와 기본 모델로 시작합니다. 탭에서 바꾼 값은 그 검토에만 씁니다. 아래에서 고른
                  제공자의 모델·키를 정합니다.
                </p>
                <div className="providers">
                  {shown && (
                    <ProviderSettings
                      key={shown.id}
                      p={shown}
                      model={draft.aiModels[shown.id] ?? ""}
                      onModel={(m) => change({ aiModels: { ...draft.aiModels, [shown.id]: m } })}
                      endpoint={draft.aiEndpoints[shown.id] ?? ""}
                      onEndpoint={(u) => change({ aiEndpoints: { ...draft.aiEndpoints, [shown.id]: u } })}
                    />
                  )}
                </div>
              </div>
            ),
          },
          {
            id: "collect",
            label: "새 작업 기본값",
            content: (
              <div className="settings-collect">
                <fieldset className="settings-group">
                  <legend className="field-label">다시보기와 생방송</legend>
                  <label className="check">
                    <input type="checkbox" checked={draft.sensitive} onChange={(e) => change({ sensitive: e.target.checked })} />
                    항상 가장 민감하게 (10초에 채팅 6건부터 판정합니다. 작은 반응도 찾지만 잘못 잡는 후보가 늘 수 있습니다)
                  </label>
                </fieldset>
                <fieldset className="settings-group">
                  <legend className="field-label">다시보기</legend>
                  <label className="check">
                    <input
                      type="checkbox"
                      checked={draft.includeComments}
                      onChange={(e) => change({ includeComments: e.target.checked })}
                    />
                    영상 댓글의 시청자 추천도 수집 (댓글에 적힌 시각을 자동 후보와 따로 표시합니다)
                  </label>
                </fieldset>
                <fieldset className="settings-group">
                  <legend className="field-label">생방송</legend>
                  <label className="check">
                    <input
                      type="checkbox"
                      checked={draft.viewerAdaptive}
                      onChange={(e) => change({ viewerAdaptive: e.target.checked })}
                    />
                    시청자 수 변화 보정 (시청자가 기준보다 10% 넘게 줄면 평소 채팅량 기준을 다시 학습합니다)
                  </label>
                </fieldset>
                <p className="field-help">새 작업의 수집 양식이 이 값으로 시작합니다. 양식에서 바꾼 값은 그 작업에만 씁니다.</p>
              </div>
            ),
          },
        ]}
      />
      <div className="settings-save">
        <p className="actions">
          <button type="button" className="button" onClick={() => save.mutate()} disabled={!changed || save.isPending}>
            기본값 저장
          </button>
          {changed ? (
            <span className="field-help">저장하지 않은 변경이 있습니다. API 키는 각 제공자의 ‘키 저장’으로 따로 저장합니다.</span>
          ) : (
            save.isSuccess && (
              <span className="field-help" role="status">
                기본값을 저장했습니다.
              </span>
            )
          )}
        </p>
        {save.isError && (
          <p className="error" role="alert">
            {save.error.message}
          </p>
        )}
      </div>
    </div>
  );
}

interface ProviderProps {
  p: AIProvider;
  model: string;
  onModel: (model: string) => void;
  endpoint: string;
  onEndpoint: (url: string) => void;
}

/**
 * One provider: where it sends (for a proxy), its default model and its key
 * (or, for OpenCode, whether its own login is ready).
 */
function ProviderSettings({ p, model, onModel, endpoint, onEndpoint }: ProviderProps) {
  const [models, setModels] = useState<AIModel[]>([]);
  const loadModels = useMutation({ mutationFn: () => api.aiModels(p.id), onSuccess: (r) => setModels(r.models) });
  const titleId = `provider-${p.id}`;
  const listId = `provider-${p.id}-models`;
  const stateText = p.selfAuth ? (p.configured ? "준비됨" : "준비 안 됨") : p.configured ? "키 있음" : "키 없음";
  const listButton = p.canListModels && p.configured && (
    <button type="button" className="link-button" onClick={() => loadModels.mutate()} disabled={loadModels.isPending}>
      {models.length > 0 ? `모델 ${models.length}개 불러옴` : "쓸 수 있는 모델 불러오기"}
    </button>
  );

  return (
    <section className="sheet provider" aria-labelledby={titleId}>
      <header className="provider-head">
        <h3 id={titleId} className="provider-name">
          {p.name}
        </h3>
        <span className="provider-state" data-ready={p.configured || undefined}>
          {stateText}
        </span>
      </header>
      <div className="provider-body">
        {p.defaultEndpoint && (
          <>
            <label className="field">
              <span className="field-label">{p.name} 주소</span>
              <input
                type="url"
                inputMode="url"
                value={endpoint}
                onChange={(e) => onEndpoint(e.target.value)}
                placeholder={p.defaultEndpoint}
              />
            </label>
            <p className="field-help">
              CLIProxyAPI를 실행한 컴퓨터와 포트의 OpenAI 호환 주소입니다. 비우면 {p.defaultEndpoint}로 보냅니다. 키는 CLIProxyAPI
              설정의 access.api-keys에 있는 키를 넣습니다. 어느 서비스·계정으로 보낼지는 CLIProxyAPI와 모델 ID가 정하며, 구독
              계정을 API처럼 쓰는 것은 그 서비스의 약관에 어긋날 수 있습니다.
            </p>
          </>
        )}
        <label className="field">
          <span className="field-label">기본 모델 ID</span>
          <input
            type="text"
            value={model}
            onChange={(e) => onModel(e.target.value)}
            list={listId}
            placeholder={p.defaultModel || "계정에서 쓸 수 있는 모델 ID"}
          />
          <datalist id={listId}>
            {models.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name}
              </option>
            ))}
          </datalist>
        </label>
        <p className="field-help">
          {p.defaultModel ? `비우면 ${p.defaultModel}을 씁니다.` : "비우면 AI 검토에서 모델 ID를 직접 입력합니다."}
        </p>
        {p.selfAuth ? (
          <div className="provider-auth">
            <p className="ai-auth" data-ready={p.configured || undefined}>
              {p.configured ? "자체 로그인으로 보냄 (계정과 구독은 실행할 때 확인)" : p.notReady}
            </p>
            {p.authHelp && <p className="field-help">{p.authHelp}</p>}
            {listButton && <p className="actions">{listButton}</p>}
          </div>
        ) : (
          <AIKeyField key={p.id} provider={p} extra={listButton} />
        )}
        {loadModels.isError && (
          <p className="error" role="alert">
            {loadModels.error.message}
          </p>
        )}
      </div>
    </section>
  );
}
