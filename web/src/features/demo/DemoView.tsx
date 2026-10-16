import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import { queryState, useAppState } from "../../shared/appState";
import { CandidateList } from "../../shared/candidates/CandidateList";
import { Link } from "../../shared/navigation";
import { Timeline } from "../../shared/timeline/Timeline";

export function DemoView() {
  const demo = useQuery({ queryKey: ["demo"], queryFn: api.demo });
  const [selectedId, setSelectedId] = useState<string | null>(null);
  useAppState(queryState(demo));
  const select = (id: string) => setSelectedId((cur) => (cur === id ? null : id));

  return (
    <main className="page">
      <p className="back">
        <Link href="/">처음으로</Link>
      </p>
      <header>
        <h1 className="page-title">샘플 채팅</h1>
        <p className="page-lede">
          실제 방송이 아닌 10분짜리 예시 채팅을 분석한 결과입니다. 후보를 누르면 타임라인에서 위치와 근거 채팅을
          보여줍니다.
        </p>
      </header>

      {demo.isError && (
        <p className="error section" role="alert">
          {demo.error.message}
        </p>
      )}
      {demo.isPending && <p className="empty section">분석하는 중입니다.</p>}
      {demo.data && (
        <>
          <section className="section" aria-labelledby="timeline-title">
            <h2 id="timeline-title" className="section-title">
              반응 타임라인
            </h2>
            <Timeline
              durationMs={demo.data.durationMs}
              windows={demo.data.windows}
              candidates={demo.data.candidates}
              selectedId={selectedId}
              onSelect={select}
            />
          </section>
          <section className="section" aria-labelledby="candidates-title">
            <h2 id="candidates-title" className="section-title">
              편집 후보 {demo.data.candidates.length}개
            </h2>
            <CandidateList candidates={demo.data.candidates} selectedId={selectedId} onSelect={select} />
          </section>
        </>
      )}
    </main>
  );
}
