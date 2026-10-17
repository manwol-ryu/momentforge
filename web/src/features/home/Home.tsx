import { useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { Session } from "../../api/types";
import { queryState, useAppState } from "../../shared/appState";
import { JobPanel } from "../../shared/job/JobPanel";
import { RevealWords } from "../../shared/motion/RevealWords";
import { Tabs } from "../../shared/motion/Tabs";
import { navigate, useDocumentTitle } from "../../shared/navigation";
import { isRunningLive } from "../../shared/sessionText";
import { useJob } from "../../shared/useJob";
import { CollectForm } from "./CollectForm";
import { LiveForm } from "./LiveForm";

type Mode = "vod" | "live";

const modeName: Record<Mode, string> = { vod: "다시보기", live: "생방송" };

/** What the address field of either tab shares: one address, carried between the tabs. */
export interface UrlFieldProps {
  url: string;
  onUrl: (url: string) => void;
  onPaste: (text: string) => void;
  /** Take the focus (the tab was just switched to by a paste). */
  focus: boolean;
  /** Say that a paste switched the tab. */
  moved: boolean;
  /** A note when the address looks like the other tab's. */
  hint?: ReactNode;
}

/**
 * The kind of broadcast an address points at, judged by its path: a
 * "/video/…" or "/videos/…" segment is a VOD, "/live/…" a live channel.
 * Only a convenience for choosing the tab; the server decides.
 */
function guessKind(text: string): Mode | null {
  try {
    const parts = new URL(text.trim()).pathname.split("/");
    if (parts.includes("video") || parts.includes("videos")) return "vod";
    if (parts.includes("live")) return "live";
  } catch {
    // not an address (yet)
  }
  return null;
}

/** The start screen: collect a VOD or a live broadcast, or open the sample. */
export function Home() {
  const status = useQuery({ queryKey: ["status"], queryFn: api.status });
  const sessions = useQuery({ queryKey: ["sessions"], queryFn: api.sessions });
  const job = useJob();
  const [mode, setMode] = useState<Mode>("vod");
  const [url, setUrl] = useState("");
  const [moved, setMoved] = useState(false);
  const runningLive = sessions.data?.find(isRunningLive);
  useDocumentTitle("새 작업");
  useAppState(queryState({ isSuccess: status.isSuccess && sessions.isSuccess, isError: status.isError || sessions.isError }));

  const choose = (m: Mode) => {
    setMode(m);
    setMoved(false);
  };
  const guessed = guessKind(url);
  const field = (m: Mode): UrlFieldProps => ({
    url,
    onUrl: setUrl,
    // A pasted address of the other kind switches the tab and keeps the address.
    onPaste: (text) => {
      const kind = guessKind(text);
      if (kind && kind !== m) {
        setMode(kind);
        setMoved(true);
      }
    },
    focus: moved && mode === m,
    moved: moved && mode === m,
    hint: guessed && guessed !== m && (
      <p className="field-hint">
        {modeName[guessed]} 주소로 보입니다.{" "}
        <button type="button" className="link-button" onClick={() => choose(guessed)}>
          {modeName[guessed]} 수집으로 옮기기
        </button>
      </p>
    ),
  });

  return (
    <div className="page-start">
      <header className="start-hero">
        <h1 className="display">
          <RevealWords text="새 작업" />
        </h1>
        <p className="page-lede">
          <RevealWords text="방송 채팅에서 편집할 순간을 찾습니다." start={2} />
        </p>
      </header>

      <div className="start-grid">
        <div className="start-main">
          <div className="frame">
            <Tabs
              label="수집할 방송"
              className="start-tabs"
              value={mode}
              onChange={choose}
              tabs={[
                { id: "vod", label: "다시보기", content: <CollectForm {...field("vod")} busy={job.data?.busy ?? false} /> },
                { id: "live", label: "생방송", content: <LiveForm {...field("live")} running={runningLive} /> },
              ]}
            />
          </div>
          {job.data?.id && <JobPanel job={job.data} />}

          <section className="steps" aria-labelledby="steps-title">
            <h2 id="steps-title" className="section-title">
              이렇게 진행됩니다
            </h2>
            <ol className="step-list">
              <li className="step" data-tint="blue">
                <h3 className="step-title">주소 넣기</h3>
                <p>치지직 다시보기나 생방송 주소를 붙여넣으면 맞는 탭으로 옮겨 갑니다.</p>
              </li>
              <li className="step" data-tint="lilac">
                <h3 className="step-title">채팅 수집과 분석</h3>
                <p>채팅을 받아 평소 채팅량과 비교해 반응이 몰린 구간을 찾습니다. 작성자는 가명으로만 저장합니다.</p>
              </li>
              <li className="step" data-tint="mint">
                <h3 className="step-title">후보 확인과 내보내기</h3>
                <p>타임라인과 후보 목록에서 장면을 확인하고 TXT·CSV 파일로 받습니다.</p>
              </li>
            </ol>
          </section>
        </div>

        <aside className="start-side" aria-label="도움말과 앱 정보">
          <section className="sheet" data-tint="peach" aria-labelledby="sample-title">
            <h2 id="sample-title" className="sheet-title">
              처음이라면
            </h2>
            <p className="field-help">실제 방송 없이 예시 채팅으로 반응 타임라인, 편집 후보, 내보내기를 먼저 살펴봅니다.</p>
            <p className="actions">
              <DemoButton existing={sessions.data?.find((s) => s.isDemo)} />
            </p>
          </section>
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
                <dt>지원 플랫폼</dt>
                <dd>{status.data?.platforms.join(", ") || "확인하는 중"}</dd>
                <dt>데이터 폴더</dt>
                <dd>{status.data?.dataDir ?? "확인하는 중"}</dd>
                <dt>저장소 형식</dt>
                <dd className="timecode">{status.data ? `스키마 ${status.data.schemaVersion}` : "확인하는 중"}</dd>
              </dl>
            )}
          </section>
        </aside>
      </div>
    </div>
  );
}

// Opens the sample, creating it the first time.
function DemoButton({ existing }: { existing?: Session }) {
  const queryClient = useQueryClient();
  const demo = useMutation({
    mutationFn: () => api.demo(),
    onSuccess: (s) => {
      void queryClient.invalidateQueries({ queryKey: ["sessions"] });
      navigate(`/sessions/${s.id}`);
    },
  });
  return (
    <>
      <button
        type="button"
        className="button button-quiet"
        onClick={() => (existing ? navigate(`/sessions/${existing.id}`) : demo.mutate())}
        disabled={demo.isPending}
      >
        샘플로 먼저 체험
      </button>
      {demo.isError && (
        <span className="error" role="alert">
          {" "}
          {demo.error.message}
        </span>
      )}
    </>
  );
}
