import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { AIModel, AIProvider, AIService, AITask, ASREngine, Settings, SettingsInfo } from "../../api/types";
import { AIKeyField } from "../../shared/ai/AIKeyField";
import { queryState, useAppState } from "../../shared/appState";
import { RevealWords } from "../../shared/motion/RevealWords";
import { Segmented } from "../../shared/motion/Segmented";
import { Tabs } from "../../shared/motion/Tabs";
import { useDocumentTitle } from "../../shared/navigation";
import { useSettings } from "../../shared/settings";

type Section = "ai" | "asr" | "collect" | "backup";

/** Only the non-empty values of a map, in a stable order (empty means "the provider's own"). */
function filled(m: Record<string, string> | undefined): Record<string, string> {
  const out: Record<string, string> = {};
  for (const id of Object.keys(m ?? {}).sort()) {
    const v = m![id]!.trim();
    if (v) out[id] = v;
  }
  return out;
}

/** Only the non-empty paths of each engine, engines without any left out. */
function filledPaths(m: Record<string, Record<string, string>> | undefined): Record<string, Record<string, string>> {
  const out: Record<string, Record<string, string>> = {};
  for (const id of Object.keys(m ?? {}).sort()) {
    const paths = filled(m![id]);
    if (Object.keys(paths).length > 0) out[id] = paths;
  }
  return out;
}

/** Only the non-empty models per task of each provider, providers without any left out. */
function filledTasks(m: Settings["aiTaskModels"] | undefined): Settings["aiTaskModels"] {
  const out: Settings["aiTaskModels"] = {};
  for (const id of Object.keys(m ?? {}).sort()) {
    const tasks: Partial<Record<AITask, string>> = {};
    for (const task of aiTasks) {
      const v = m![id]?.[task]?.trim();
      if (v) tasks[task] = v;
    }
    if (Object.keys(tasks).length > 0) out[id] = tasks;
  }
  return out;
}

/** Only the values, with empty models, addresses and paths left out. */
function valuesOf(s: Settings): Settings {
  return {
    aiProvider: s.aiProvider,
    aiModels: filled(s.aiModels),
    aiEndpoints: filled(s.aiEndpoints),
    sensitive: s.sensitive,
    includeComments: s.includeComments,
    viewerAdaptive: s.viewerAdaptive,
    asrEngine: s.asrEngine ?? "",
    asrPaths: filledPaths(s.asrPaths),
    cutSearchRounds: s.cutSearchRounds ?? 4,
    cutSearchKb: s.cutSearchKb ?? 80,
    cutConfirmSearches: s.cutConfirmSearches ?? false,
    backupDir: (s.backupDir ?? "").trim(),
    backupVideo: s.backupVideo ?? true,
    backupVideoHeight: s.backupVideoHeight || 720,
    autoAI: s.autoAI ?? false,
    autoAIMaxRequests: s.autoAIMaxRequests || 60,
    autoAICostCapUsd: s.autoAICostCapUsd || 3,
    autoTranscriptFix: s.autoTranscriptFix ?? true,
    autoTranscribe: s.autoTranscribe ?? true,
    liveAudio: s.liveAudio ?? true,
    aiTaskModels: filledTasks(s.aiTaskModels),
  };
}

const aiTasks: AITask[] = ["transcript", "explain", "cut"];

/** Each task's name and what kind of model fits it, as the settings say it. */
const taskText: Record<AITask, { name: string; help: string }> = {
  transcript: { name: "대본 다듬기", help: "요청이 많고 짧아 빠르고 싼 모델이 맞습니다." },
  explain: { name: "장면 설명", help: "채팅과 발언을 함께 읽는 균형 잡힌 모델이 맞습니다." },
  cut: { name: "컷 분석", help: "근거를 따져 구간을 정하는 가장 강한 모델이 맞습니다." },
};

/**
 * The settings screen: the AI provider and models the AI review starts
 * with, each provider's key (memory, or kept on this computer), and the
 * options the new-task forms start with. Defaults go to the data folder's
 * settings.env; keys never do.
 */
export function SettingsView() {
  const providers = useQuery({ queryKey: ["aiProviders"], queryFn: api.aiProviders });
  const settings = useSettings();
  const status = useQuery({ queryKey: ["status"], queryFn: api.status });
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
          <RevealWords text="편집 후보가 나오면 저절로 할 일과 새 작업·AI 검토의 기본값을 정합니다." start={1} />
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

        <aside className="start-side" aria-label="앱 정보와 저장 위치">
          <section className="sheet" aria-labelledby="app-title">
            <h2 id="app-title" className="sheet-title">
              앱 정보
            </h2>
            {status.isError ? (
              <p className="error" role="alert">
                {status.error.message}
              </p>
            ) : (
              <dl className="facts facts-stacked">
                <dt>버전</dt>
                <dd>{status.data?.version ?? "확인하는 중"}</dd>
                <dt>지원 플랫폼</dt>
                <dd>{status.data?.platforms.join(", ") || "확인하는 중"}</dd>
                <dt>데이터 폴더</dt>
                <dd>{status.data?.dataDir ?? "확인하는 중"}</dd>
                <dt>저장소 형식</dt>
                <dd className="timecode">{status.data ? `스키마 ${status.data.schemaVersion}` : "확인하는 중"}</dd>
              </dl>
            )}
          </section>
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
              두 파일 모두 데이터 폴더 안에 있어 Git에 올라가지 않고, 나만 읽을 수 있는 권한으로 씁니다. API 키는 앱이 실행되는 동안
              메모리에 두고, ‘이 컴퓨터에도 저장’을 고른 키만 .env 파일에 평문으로 남습니다. 키를 넣어도 일반 설정에서 AI 검토 자동
              실행을 켜거나 작업 화면에서 직접 보내기 전에는 아무것도 보내지 않습니다.
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
  const [section, setSection] = useState<Section>("collect");
  const save = useMutation({
    mutationFn: () => api.saveSettings(valuesOf(draft)),
    onSuccess: (s) => {
      queryClient.setQueryData(["settings"], s);
      setDraft(valuesOf(s));
      // A saved address changes where a provider sends; saved paths change whether an engine is ready.
      void queryClient.invalidateQueries({ queryKey: ["aiProviders"] });
      void queryClient.invalidateQueries({ queryKey: ["asrEngines"] });
    },
  });
  const changed = JSON.stringify(valuesOf(draft)) !== JSON.stringify(valuesOf(saved));
  const change = (patch: Partial<Settings>) => {
    save.reset();
    setDraft((d) => ({ ...d, ...patch }));
  };
  const defaultProvider = providers.some((p) => p.id === draft.aiProvider) ? draft.aiProvider : (providers[0]?.id ?? "");
  // Which provider's address, model and key are shown: any of them, without making it the default.
  const [viewing, setViewing] = useState(defaultProvider);
  const shown = providers.find((p) => p.id === viewing) ?? providers.find((p) => p.id === defaultProvider);
  const defaultName = providers.find((p) => p.id === defaultProvider)?.name ?? "";

  return (
    <div className="settings">
      <Tabs
        label="설정 종류"
        className="start-tabs"
        value={section}
        onChange={setSection}
        tabs={[
          {
            id: "collect",
            label: "일반 설정",
            content: (
              <div className="settings-collect">
                <AutoSettings draft={draft} saved={saved} providers={providers} change={change} />
                <fieldset className="settings-group">
                  <legend className="field-label">다시보기와 생방송</legend>
                  <label className="check check-option">
                    <input type="checkbox" checked={draft.sensitive} onChange={(e) => change({ sensitive: e.target.checked })} />
                    <span>
                      항상 가장 민감하게
                      <span className="check-help">10초에 채팅 6건부터 판정합니다. 작은 반응도 찾지만 잘못 잡는 후보가 늘 수 있습니다.</span>
                    </span>
                  </label>
                </fieldset>
                <fieldset className="settings-group">
                  <legend className="field-label">다시보기</legend>
                  <label className="check check-option">
                    <input
                      type="checkbox"
                      checked={draft.includeComments}
                      onChange={(e) => change({ includeComments: e.target.checked })}
                    />
                    <span>
                      영상 댓글의 시청자 추천도 수집
                      <span className="check-help">댓글에 적힌 시각을 자동 후보와 따로 표시합니다.</span>
                    </span>
                  </label>
                </fieldset>
                <fieldset className="settings-group">
                  <legend className="field-label">생방송</legend>
                  <label className="check check-option">
                    <input
                      type="checkbox"
                      checked={draft.viewerAdaptive}
                      onChange={(e) => change({ viewerAdaptive: e.target.checked })}
                    />
                    <span>
                      시청자 수 변화 보정
                      <span className="check-help">시청자가 기준보다 10% 넘게 줄면 평소 채팅량 기준을 다시 학습합니다.</span>
                    </span>
                  </label>
                </fieldset>
                <p className="field-help">새 작업의 수집 양식이 이 값으로 시작합니다. 양식에서 바꾼 값은 그 작업에만 씁니다.</p>
              </div>
            ),
          },
          {
            id: "ai",
            label: "AI 검토",
            content: (
              <div className="settings-ai">
                {providers.length > 1 && (
                  <Segmented
                    name="shown-provider"
                    legend="설정할 AI 제공자"
                    value={shown?.id ?? defaultProvider}
                    onChange={setViewing}
                    options={providers.map((p) => ({
                      value: p.id,
                      label: (
                        <>
                          {p.name}
                          {p.id === defaultProvider && (
                            <span className="seg-mark" aria-hidden="true">
                              기본
                            </span>
                          )}
                        </>
                      ),
                    }))}
                  />
                )}
                <p className="field-help">
                  제공자를 고르면 그 제공자의 주소·모델·키를 정합니다. 자동 AI 검토, AI 장면 설명 받기, AI 컷 분석은 모두 ‘기본’ 표시가
                  붙은 제공자로 보내고, 작업마다 그 제공자의 작업별 모델을 씁니다.
                </p>
                {saved.aiProviderAuto && draft.aiProvider === saved.aiProvider && (
                  <p className="auto-note" role="note">
                    이 컴퓨터에서 바로 쓸 수 있는 {defaultName}을(를) 자동으로 골랐습니다. 다른 제공자의 ‘기본 제공자로 쓰기’를 누르고
                    기본값을 저장하면 그것을 씁니다.
                  </p>
                )}
                <div className="providers">
                  {shown && (
                    <ProviderSettings
                      key={shown.id}
                      p={shown}
                      isDefault={shown.id === defaultProvider}
                      defaultName={defaultName}
                      onMakeDefault={() => change({ aiProvider: shown.id })}
                      model={draft.aiModels[shown.id] ?? ""}
                      onModel={(m) => change({ aiModels: { ...draft.aiModels, [shown.id]: m } })}
                      taskModels={draft.aiTaskModels[shown.id] ?? {}}
                      onTaskModel={(task, m) =>
                        change({ aiTaskModels: { ...draft.aiTaskModels, [shown.id]: { ...draft.aiTaskModels[shown.id], [task]: m } } })
                      }
                      endpoint={draft.aiEndpoints[shown.id] ?? ""}
                      onEndpoint={(u) => change({ aiEndpoints: { ...draft.aiEndpoints, [shown.id]: u } })}
                    />
                  )}
                </div>
                <fieldset className="settings-group search-budget">
                  <legend className="field-label">컷 분석의 검색 예산 (후보당)</legend>
                  <p className="field-help">
                    컷 분석이 "아까"·약속 같은 앞선 맥락을 물으면 이 방송 안의 발언·채팅을 검색해 더 보내고 분석을 다시 합니다. 처음 보내는
                    범위(편집 구간 앞뒤 1분) 밖에서 얼마나 더 보낼 수 있는지의 상한입니다. 검색 단계를 0으로 두면 검색하지 않습니다.
                  </p>
                  <div className="search-budget-fields">
                    <NumberField
                      label="검색 단계"
                      unit="회 (0~10, 기본 4)"
                      min={0}
                      max={10}
                      step={1}
                      value={draft.cutSearchRounds}
                      onChange={(n) => change({ cutSearchRounds: n })}
                    />
                    <NumberField
                      label="검색으로 더 보내는 글"
                      unit="KB (0~1024, 기본 80)"
                      min={0}
                      max={1024}
                      step={10}
                      value={draft.cutSearchKb}
                      onChange={(n) => change({ cutSearchKb: n })}
                    />
                  </div>
                  <label className="check check-option">
                    <input
                      type="checkbox"
                      checked={draft.cutConfirmSearches}
                      onChange={(e) => change({ cutConfirmSearches: e.target.checked })}
                    />
                    <span>
                      검색 결과를 보내기 전에 매번 확인
                      <span className="check-help">검색이 찾은 발언·채팅을 후보 목록 위 AI 작업 줄에 보여 주고, 보낼지 고를 때까지 기다립니다.</span>
                    </span>
                  </label>
                </fieldset>
              </div>
            ),
          },
          {
            id: "asr",
            label: "방송 내용 인식",
            content: (
              <ASRSettings
                engine={draft.asrEngine}
                onEngine={(id) => change({ asrEngine: id })}
                paths={draft.asrPaths}
                onPath={(id, key, value) =>
                  change({ asrPaths: { ...draft.asrPaths, [id]: { ...(draft.asrPaths[id] ?? {}), [key]: value } } })
                }
              />
            ),
          },
          {
            id: "backup",
            label: "백업 묶음",
            content: (
              <div className="settings-collect">
                <fieldset className="settings-group">
                  <legend className="field-label">묶음을 둘 폴더</legend>
                  <label className="field">
                    <span className="field-label">폴더 경로</span>
                    <input
                      type="text"
                      value={draft.backupDir}
                      onChange={(e) => change({ backupDir: e.target.value })}
                      placeholder={saved.backupDefaultDir}
                      spellCheck={false}
                      autoComplete="off"
                    />
                  </label>
                  <p className="field-help">
                    비우면 데이터 폴더의 backups에 둡니다. NAS에 두려면 마운트한 공유 폴더의 전체 경로(예: /Volumes/공유/momentforge)를
                    적습니다. 작업마다 이 폴더 안에 작업 ID 이름의 폴더를 만듭니다.
                  </p>
                </fieldset>
                <fieldset className="settings-group">
                  <legend className="field-label">영상</legend>
                  <label className="check check-option">
                    <input type="checkbox" checked={draft.backupVideo} onChange={(e) => change({ backupVideo: e.target.checked })} />
                    <span>
                      새 묶음에 영상 포함
                      <span className="check-help">
                        치지직 다시보기를 재인코딩 없이 video.mp4로 받습니다. 720p 3시간이 약 4GB입니다. NAS 백업에서 불러온 작업은 영상을
                        복사하지 않고 경로만 적습니다.
                      </span>
                    </span>
                  </label>
                  <Segmented
                    name="backup-height"
                    legend="받을 화질 (이 화질 이하에서 가장 높은 것)"
                    value={String(draft.backupVideoHeight)}
                    onChange={(v) => change({ backupVideoHeight: Number(v) })}
                    options={(saved.backupVideoHeights ?? [1080, 720, 480, 360]).map((h) => ({ value: String(h), label: `${h}p` }))}
                  />
                </fieldset>
                <p className="field-help">작업 화면의 파일 탭에서 ‘백업 묶음 만들기’를 누르면 이 값으로 시작합니다.</p>
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

interface AutoProps {
  draft: Settings;
  saved: SettingsInfo;
  providers: AIProvider[];
  change: (patch: Partial<Settings>) => void;
}

/**
 * What runs by itself once candidates are found (docs/REDESIGN.md 15,
 * 2026-10-22): a VOD's transcript, a capture's audio for its candidates,
 * and the AI review. Turning the AI review on asks first, in place, what
 * it will send and within which limits: from then on nothing is previewed.
 */
function AutoSettings({ draft, saved, providers, change }: AutoProps) {
  const [asking, setAsking] = useState(false);
  const provider = providers.find((p) => p.id === draft.aiProvider) ?? providers[0];
  const priced = provider?.hasPrices ?? false;
  return (
    <fieldset className="settings-group auto-settings">
      <legend className="field-label">편집 후보가 나오면 자동으로</legend>
      <label className="check check-option">
        <input type="checkbox" checked={draft.autoTranscribe} onChange={(e) => change({ autoTranscribe: e.target.checked })} />
        <span>
          다시보기 방송 내용 인식
          <span className="check-help">
            수집과 분석이 끝나면 방송 음성을 가장 낮은 화질로 받아 이 컴퓨터에서 대본으로 만듭니다(3시간 방송에 약 8분, 받은 음성은
            인식 뒤 지움). AI 검토가 이 대본을 함께 읽습니다.
          </span>
        </span>
      </label>
      <label className="check check-option">
        <input type="checkbox" checked={draft.liveAudio} onChange={(e) => change({ liveAudio: e.target.checked })} />
        <span>
          생방송 음성도 받아 후보 구간 인식
          <span className="check-help">
            수집하는 동안 방송 음성을 가장 낮은 화질로 받아 최근 25분만 이 컴퓨터에 두고, 반응 5분 뒤 판정한 후보의 구간을 대본으로
            만듭니다. 수집이 끝나면 음성은 지웁니다.
          </span>
        </span>
      </label>
      <label className="check check-option">
        <input
          type="checkbox"
          checked={draft.autoAI || asking}
          onChange={(e) => {
            if (!e.target.checked) {
              setAsking(false);
              change({ autoAI: false });
            } else if (saved.autoAI) {
              change({ autoAI: true });
            } else {
              setAsking(true);
            }
          }}
        />
        <span>
          AI 검토 자동 실행
          <span className="check-help">
            편집 후보가 나오면 묻지 않고 기본 AI 제공자{provider ? `(${provider.name})` : ""}로 보내 후보마다 장면 설명을 받습니다(생방송은
            반응 5분 뒤 판정한 후보부터). 끄면 작업 화면의 &lsquo;AI 장면 설명 받기&rsquo;로 작업마다 받습니다.
          </span>
        </span>
      </label>
      {asking && !draft.autoAI && (
        <div className="confirm auto-consent" role="group" aria-label="AI 검토 자동 실행 확인">
          <div className="confirm-question">
            <p>켜면 편집 후보가 나올 때마다 묻지 않고 보냅니다.</p>
            <ul>
              <li>보내는 것: 후보 앞뒤의 채팅(작성자는 가명), 방송 대본(인식한 발언), 대본을 다듬을 때 방송 제목과 시청자가 많이 쓴 낱말</li>
              <li>보내지 않는 것: 영상·음성, 원래 아이디·닉네임, API 키 밖의 계정 정보</li>
              <li>
                한도: 작업마다 요청 {draft.autoAIMaxRequests}회{priced ? `, 비용 ${draft.autoAICostCapUsd}달러` : ""}까지. 아래에서
                바꿀 수 있습니다.
              </li>
            </ul>
          </div>
          <span className="confirm-actions">
            <button type="button" className="button button-quiet" onClick={() => setAsking(false)}>
              켜지 않기
            </button>
            <button
              type="button"
              className="button"
              onClick={() => {
                setAsking(false);
                change({ autoAI: true });
              }}
            >
              동의하고 켜기
            </button>
          </span>
        </div>
      )}
      {(draft.autoAI || asking) && (
        <div className="auto-ai-options">
          <label className="check check-option">
            <input
              type="checkbox"
              checked={draft.autoTranscriptFix}
              onChange={(e) => change({ autoTranscriptFix: e.target.checked })}
            />
            <span>
              AI로 대본 다듬기
              <span className="check-help">
                인식한 대본을 앞뒤 발언, 방송 제목, 시청자가 쓴 낱말과 함께 보내 잘못 들은 말을 고칩니다. 인식한 원문은 함께 남깁니다.
              </span>
            </span>
          </label>
          <div className="search-budget-fields">
            <NumberField
              label="작업마다 최대 요청"
              unit="회 (1~500, 기본 60)"
              min={1}
              max={500}
              step={10}
              value={draft.autoAIMaxRequests}
              onChange={(n) => change({ autoAIMaxRequests: n })}
            />
            <NumberField
              label="작업마다 비용 한도"
              unit="미국 달러 (가격을 아는 모델만, 기본 3)"
              min={0.01}
              max={1000}
              step={0.5}
              decimals
              value={draft.autoAICostCapUsd}
              onChange={(n) => change({ autoAICostCapUsd: n })}
            />
          </div>
        </div>
      )}
    </fieldset>
  );
}

interface NumberFieldProps {
  label: string;
  unit: string;
  min: number;
  max: number;
  step: number;
  value: number;
  onChange: (value: number) => void;
  /** Takes fractions too (a dollar limit); whole numbers otherwise. */
  decimals?: boolean;
}

/**
 * A whole number in a range. What is typed stays as typed while it is
 * being edited (a cleared field is not turned into 0 at once); each
 * number in range is taken as it is typed, and leaving the field puts the
 * value in use back in it.
 */
function NumberField({ label, unit, min, max, step, value, onChange, decimals = false }: NumberFieldProps) {
  const [text, setText] = useState(String(value));
  const [editing, setEditing] = useState(false);
  const shownText = editing ? text : String(value);
  const fits = (t: string) => {
    const v = Number(t);
    return t.trim() !== "" && Number.isFinite(v) && (decimals || Number.isInteger(v)) && v >= min && v <= max;
  };
  const invalid = editing && !fits(text);
  return (
    <label className="field">
      <span className="field-label">{label}</span>
      <input
        type="number"
        inputMode="numeric"
        min={min}
        max={max}
        step={step}
        value={shownText}
        aria-invalid={invalid || undefined}
        onFocus={() => {
          setText(String(value));
          setEditing(true);
        }}
        onBlur={() => setEditing(false)}
        onChange={(e) => {
          const t = e.target.value;
          setText(t);
          if (fits(t)) onChange(Number(t));
        }}
      />
      <span className={invalid ? "field-help field-invalid" : "field-help"}>
        {invalid ? `${min}~${max} 사이의 ${decimals ? "숫자" : "정수"}를 넣으세요. 지금 쓰는 값은 ${value}입니다.` : unit}
      </span>
    </label>
  );
}

interface ASRProps {
  engine: string;
  onEngine: (id: string) => void;
  paths: Record<string, Record<string, string>>;
  onPath: (engine: string, key: string, value: string) => void;
}

/**
 * Speech recognition (docs/AI-CUT-ANALYSIS.md 2.2): every registered
 * engine with whether it can run now and what to do when it cannot, the
 * engine a transcription starts with, and the paths an installed program
 * (whisper.cpp) needs. Recognition runs on this computer only; nothing here
 * downloads a program or a model.
 */
function ASRSettings({ engine, onEngine, paths, onPath }: ASRProps) {
  const engines = useQuery({ queryKey: ["asrEngines"], queryFn: api.asrEngines });
  const list = engines.data?.engines ?? [];
  const chosen = list.some((e) => e.id === engine) ? engine : "";
  const auto = list.find((e) => e.default);

  if (engines.isError) {
    return (
      <p className="error" role="alert">
        {engines.error.message}
      </p>
    );
  }
  if (!engines.data) {
    return (
      <p className="field-help" aria-busy="true">
        음성 인식 엔진을 확인하는 중
      </p>
    );
  }
  return (
    <div className="settings-asr">
      <p className="field-help">
        방송자가 한 말을 글로 바꾸는 일은 이 컴퓨터에서만 합니다. 음성은 어디로도 보내지 않고, 모델도 MomentForge가 내려받지 않습니다.
      </p>
      {list.length === 0 ? (
        <p className="empty">이 실행파일에는 음성 인식 엔진이 없습니다.</p>
      ) : (
        <>
          <Segmented
            name="default-asr"
            legend="기본 음성 인식 엔진"
            value={chosen}
            onChange={onEngine}
            options={[{ value: "", label: "자동" }, ...list.map((e) => ({ value: e.id, label: e.name }))]}
          />
          <p className="field-help">
            {chosen === ""
              ? auto
                ? `자동: 지금은 준비된 첫 엔진인 ${auto.name}을(를) 씁니다.`
                : "자동: 준비된 엔진이 생기면 그것을 씁니다. 지금은 쓸 수 있는 엔진이 없습니다."
              : "고른 엔진이 준비되지 않았으면 준비된 다른 엔진을 씁니다."}
          </p>
          <ul className="engines" aria-label="음성 인식 엔진">
            {list.map((e) => (
              <li key={e.id}>
                <EngineCard e={e} paths={paths[e.id] ?? {}} onPath={(key, value) => onPath(e.id, key, value)} />
              </li>
            ))}
          </ul>
          <p className="actions">
            <button type="button" className="link-button" onClick={() => void engines.refetch()} disabled={engines.isFetching}>
              준비 상태 다시 확인
            </button>
          </p>
        </>
      )}
    </div>
  );
}

/** One engine: its state, the way to get it ready, and its paths when it takes any. */
function EngineCard({ e, paths, onPath }: { e: ASREngine; paths: Record<string, string>; onPath: (key: string, value: string) => void }) {
  const titleId = `engine-${e.id}`;
  return (
    <section className="sheet engine" aria-labelledby={titleId} data-engine={e.id}>
      <header className="provider-head">
        <h3 id={titleId} className="provider-name">
          {e.name}
          {e.default && <span className="engine-default"> · 지금 쓰는 엔진</span>}
        </h3>
        <span className="provider-state" data-ready={e.ready || undefined}>
          {e.ready ? "준비됨" : "준비 안 됨"}
        </span>
      </header>
      <div className="provider-body">
        {e.how && <p className={e.ready ? "field-help" : "ai-auth engine-how"}>{e.how}</p>}
        {e.settings?.map((st) => (
          <div key={st.key} className="engine-setting">
            <label className="field">
              <span className="field-label">{st.label}</span>
              <input
                type="text"
                value={paths[st.key] ?? ""}
                onChange={(ev) => onPath(st.key, ev.target.value)}
                placeholder={st.placeholder}
                spellCheck={false}
                autoComplete="off"
              />
            </label>
            {st.help && <p className="field-help">{st.help}</p>}
          </div>
        ))}
        {e.settings && e.settings.length > 0 && (
          <p className="field-help">경로를 적고 아래 ‘기본값 저장’을 누르면 준비 상태를 다시 확인합니다.</p>
        )}
      </div>
    </section>
  );
}

interface ProviderProps {
  p: AIProvider;
  /** Whether the AI review starts with this provider, and the way to make it so. */
  isDefault: boolean;
  defaultName: string;
  onMakeDefault: () => void;
  model: string;
  onModel: (model: string) => void;
  /** The model set per task (empty: the recommendation, else model). */
  taskModels: Partial<Record<AITask, string>>;
  onTaskModel: (task: AITask, model: string) => void;
  endpoint: string;
  onEndpoint: (url: string) => void;
}

/**
 * One provider: where it sends (for a proxy), its default model and its key
 * (or, for a provider without one, whether it is ready).
 */
function ProviderSettings({ p, isDefault, defaultName, onMakeDefault, model, onModel, taskModels, onTaskModel, endpoint, onEndpoint }: ProviderProps) {
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
        {isDefault ? (
          <p className="provider-default" data-default>
            기본 제공자입니다. AI 검토를 이 제공자로 보냅니다.
          </p>
        ) : (
          <p className="provider-default">
            <button type="button" className="button button-quiet" onClick={onMakeDefault}>
              기본 제공자로 쓰기
            </button>
            <span className="field-help">지금 기본 제공자는 {defaultName}입니다. 누른 뒤 기본값을 저장하면 바뀝니다.</span>
          </p>
        )}
        {p.hasService && <ServiceRow p={p} />}
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
            {p.endpointHelp && (
              <details className="field-more">
                <summary>주소 설명</summary>
                <p className="field-help">{p.endpointHelp}</p>
              </details>
            )}
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
          {p.defaultModel ? `비우면 ${p.defaultModel}을 씁니다.` : "작업별 모델을 정하지 않은 작업이 이 모델로 보냅니다."}
        </p>
        <fieldset className="settings-group task-models">
          <legend className="field-label">작업별 모델</legend>
          <p className="field-help">
            작업마다 맞는 모델이 다릅니다. 비우면 {Object.keys(p.taskModels).length > 0 ? `${p.name}이(가) 추천하는 모델을` : "위의 기본 모델을"}{" "}
            씁니다.
          </p>
          {aiTasks.map((task) => {
            const fallback = p.taskModels[task] || model.trim() || p.defaultModel;
            return (
              <label key={task} className="field">
                <span className="field-label">{taskText[task].name} 모델</span>
                <input
                  type="text"
                  value={taskModels[task] ?? ""}
                  onChange={(e) => onTaskModel(task, e.target.value)}
                  list={listId}
                  placeholder={fallback || "모델 ID"}
                />
                <span className="field-help">
                  {taskText[task].help}
                  {fallback ? ` 비우면 ${fallback}.` : ""}
                </span>
              </label>
            );
          })}
        </fieldset>
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

const serviceText = (s: AIService | undefined) =>
  !s ? "확인하는 중" : s.running ? "실행 중" : s.installed ? "꺼짐" : "설치되지 않음";

/**
 * Whether the provider's local program (CLIProxyAPI, OpenCodex) runs, the
 * way to start or stop it, and its own dashboard. Its state is asked when the card opens and after each
 * action, or when the user asks again; nothing polls.
 */
function ServiceRow({ p }: { p: AIProvider }) {
  const queryClient = useQueryClient();
  const status = useQuery({ queryKey: ["aiService", p.id], queryFn: () => api.aiService(p.id) });
  const control = useMutation({
    mutationFn: (action: "start" | "stop") => api.aiServiceControl(p.id, action),
    onSuccess: (s) => queryClient.setQueryData(["aiService", p.id], s),
    // Whether the provider can send follows its program: the provider cards and the AI review read it again.
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: ["aiService", p.id] });
      void queryClient.invalidateQueries({ queryKey: ["aiProviders"] });
    },
  });
  const s = status.data;
  const busy = control.isPending || status.isFetching;
  return (
    <div className="service" role="group" aria-labelledby={`service-${p.id}`}>
      <div className="service-head">
        <span id={`service-${p.id}`} className="field-label">
          {p.name} 실행 상태
        </span>
        <span className="service-state" data-state={s ? (s.running ? "on" : "off") : undefined} role="status">
          {control.isPending ? (control.variables === "start" ? "켜는 중" : "끄는 중") : serviceText(s)}
        </span>
      </div>
      <p className="actions">
        {s?.running ? (
          <button
            type="button"
            className="button button-quiet"
            onClick={() => control.mutate("stop")}
            disabled={busy || !s.managed}
          >
            {p.name} 끄기
          </button>
        ) : (
          <button type="button" className="button" onClick={() => control.mutate("start")} disabled={busy || !s?.installed}>
            {p.name} 켜기
          </button>
        )}
        {s?.dashboard && (
          <a className="button button-quiet" href={s.dashboard} target="_blank" rel="noreferrer">
            {p.name} 대시보드 열기
          </a>
        )}
        <button type="button" className="link-button" onClick={() => void status.refetch()} disabled={busy}>
          상태 다시 확인
        </button>
      </p>
      {s?.running && s.dashboardHelp && <p className="field-help">{s.dashboardHelp}</p>}
      {s?.running && !s.managed && (
        <p className="field-help">MomentForge가 켜지 않은 {p.name}입니다. 끄려면 실행한 터미널에서 끄세요.</p>
      )}
      {s && (
        <details className="field-more" open={!s.installed || undefined}>
          <summary>프로그램과 설정 파일 위치</summary>
        <dl className="service-paths">
          <dt>프로그램</dt>
          <dd>{s.installed ? s.program : `찾지 못함. README의 ${p.name} 준비 방법대로 설치하세요.`}</dd>
          {s.config && (
            <>
              <dt>설정 파일</dt>
              <dd>{s.config}</dd>
            </>
          )}
          {s.running && s.managed && s.log && (
            <>
              <dt>기록</dt>
              <dd>{s.log}</dd>
            </>
          )}
        </dl>
        </details>
      )}
      {s?.note && <p className="field-help service-note">{s.note}</p>}
      {(control.error ?? status.error) && (
        <p className="error" role="alert">
          {(control.error ?? status.error)!.message}
        </p>
      )}
    </div>
  );
}
