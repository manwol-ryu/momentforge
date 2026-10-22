import { useEffect, useState, type FormEvent } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { BackupBroadcast, Settings } from "../../api/types";
import { formatSize } from "../../shared/format";
import { Link } from "../../shared/navigation";

const DIR_KEY = "mf.backup.dir";

function readDir(): string {
  try {
    return localStorage.getItem(DIR_KEY) ?? "";
  } catch {
    return "";
  }
}

interface Props {
  busy: boolean;
  defaults: Settings;
  /** A job was started: the screen follows it to its saved work. */
  onStarted: (jobId: string) => void;
}

/**
 * Imports broadcasts that chzzk-automation backed up to a NAS folder
 * (docs/AI-CUT-ANALYSIS.md 2.5a): the chat file becomes a session, the
 * audio is transcribed on this computer, the video plays from where it is.
 */
export function BackupForm({ busy, defaults, onStarted }: Props) {
  const [dir, setDir] = useState(readDir);
  const [sensitive, setSensitive] = useState(defaults.sensitive);
  const [transcribe, setTranscribe] = useState(true);
  const engines = useQuery({ queryKey: ["asrEngines"], queryFn: api.asrEngines });
  const ready = engines.data?.engines.find((e) => e.ready);
  const scan = useMutation({
    mutationFn: (d: string) => api.scanBackups(d),
    onSuccess: (_r, d) => {
      try {
        localStorage.setItem(DIR_KEY, d);
      } catch {
        // storage blocked: the folder lasts until reload
      }
    },
  });
  const start = useMutation({
    mutationFn: (b: BackupBroadcast) => api.importBackup({ path: b.chatPath, transcribe: transcribe && b.hasAudio && !!ready, sensitive }),
    onSuccess: (job) => onStarted(job.id),
  });
  // A remembered folder is listed once, when the form opens; a typed one waits for "폴더 읽기".
  const [remembered] = useState(dir);
  const scanOnce = scan.mutate;
  useEffect(() => {
    if (remembered) scanOnce(remembered);
  }, [remembered, scanOnce]);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (dir.trim()) scan.mutate(dir.trim());
  };
  const list = scan.data?.broadcasts ?? [];
  const failed = scan.error ?? start.error;

  return (
    <form className="collect" onSubmit={submit} aria-label="NAS 백업 불러오기">
      <label className="field">
        <span className="field-label">백업 폴더 (이 컴퓨터에 연결된 NAS 경로)</span>
        <input
          type="text"
          value={dir}
          onChange={(e) => setDir(e.target.value)}
          placeholder="/Volumes/공유폴더/채널 이름/26/10"
          aria-describedby="backup-help"
          spellCheck={false}
        />
      </label>
      <p id="backup-help" className="field-help">
        chzzk-automation이 만든 월 폴더를 넣으세요. 폴더 안의 <code>.chat.jsonl</code>마다 방송 하나이고, 같은 이름의 <code>.m4a</code>(음성)와{" "}
        <code>.mp4</code>(영상)를 함께 씁니다. 파일은 복사하지 않고 그 자리에서 읽습니다.
      </p>
      <p className="actions">
        <button type="submit" className="button button-quiet" disabled={scan.isPending || !dir.trim()}>
          폴더 읽기
        </button>
      </p>
      <label className="check check-option">
        <input type="checkbox" checked={transcribe} onChange={(e) => setTranscribe(e.target.checked)} disabled={!ready} />
        <span>
          방송 내용 인식(전사)까지
          <span className="check-help">
            {engines.isPending
              ? "음성 인식 엔진을 확인하는 중"
              : ready
                ? `${ready.name}으로 이 컴퓨터에서 인식합니다. 음성은 밖으로 나가지 않습니다. 7시간 방송에 몇 분 걸립니다.`
                : `쓸 수 있는 음성 인식 엔진이 없습니다. ${engines.data?.engines.map((e) => e.how).filter(Boolean).join(" ") ?? ""}`}
          </span>
        </span>
      </label>
      <label className="check check-option">
        <input type="checkbox" checked={sensitive} onChange={(e) => setSensitive(e.target.checked)} />
        <span>
          항상 가장 민감하게
          <span className="check-help">10초에 채팅 6건부터 판정합니다.</span>
        </span>
      </label>
      {scan.isSuccess && list.length === 0 && <p className="empty">이 폴더에는 .chat.jsonl 파일이 없습니다.</p>}
      {list.length > 0 && (
        <ul className="backup-list" aria-label="백업된 방송">
          {list.map((b) => (
            <li key={b.chatPath} className="backup-row">
              <div className="backup-main">
                <span className="backup-title">{b.title}</span>
                <span className="backup-meta">
                  {b.date ?? ""} · 채팅 {formatSize(b.chatBytes)}
                  {b.hasAudio ? ` · 음성 ${formatSize(b.audioBytes)}` : " · 음성 없음"}
                  {b.hasVideo ? ` · 영상 ${formatSize(b.videoBytes)}` : " · 영상 없음"}
                </span>
              </div>
              {b.sessionId ? (
                <Link href={`/sessions/${b.sessionId}`} className="button button-quiet">
                  열기
                </Link>
              ) : (
                <button type="button" className="button" onClick={() => start.mutate(b)} disabled={busy || start.isPending}>
                  불러오기
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      {failed && (
        <p className="error" role="alert">
          {failed.message}
        </p>
      )}
    </form>
  );
}
