import type { Strength } from "../../api/types";
import { Segmented } from "../motion/Segmented";
import { strengthLabel, type SortOrder, type StrengthFilter } from "./strength";

interface Props {
  counts: Record<Strength, number>;
  sort: SortOrder;
  onSort: (order: SortOrder) => void;
  filter: StrengthFilter;
  onFilter: (filter: StrengthFilter) => void;
  /** The strong and medium candidates in the list that can go to the AI review, and how many are not picked yet; absent where there is no AI review. */
  pick?: { total: number; unpicked: number; onPick: () => void };
}

const strengths: Strength[] = ["strong", "medium", "weak"];

/**
 * Controls for a ranked candidate list (whole-broadcast analyses): the
 * order (time or strength), whether weak candidates are kept, how many
 * there are of each strength, and a way to pick every strong and medium
 * one for the AI review at once.
 */
export function ListTools({ counts, sort, onSort, filter, onFilter, pick }: Props) {
  return (
    <div className="list-tools">
      <Segmented
        name="candidate-sort"
        legend="후보 순서"
        hideLegend
        value={sort}
        onChange={onSort}
        options={[
          { value: "time" as SortOrder, label: "시간순" },
          { value: "strength" as SortOrder, label: "강한 순" },
        ]}
      />
      <Segmented
        name="candidate-strength"
        legend="후보 강도"
        hideLegend
        value={filter}
        onChange={onFilter}
        options={[
          { value: "all" as StrengthFilter, label: "모든 강도" },
          { value: "strong_medium" as StrengthFilter, label: "강함·보통만" },
        ]}
      />
      <p className="strength-counts">
        {strengths.map((st) => (
          <span key={st} className="strength-count" data-strength={st} data-hidden={(st === "weak" && filter === "strong_medium") || undefined}>
            {strengthLabel[st]} {counts[st]}
            {st === "weak" && filter === "strong_medium" && counts.weak > 0 && <span className="visually-hidden"> (숨김)</span>}
          </span>
        ))}
      </p>
      {pick && pick.total > 0 && (
        <button type="button" className="link-button list-pick" onClick={pick.onPick} disabled={pick.unpicked === 0}>
          {pick.unpicked > 0 ? `강함·보통 ${pick.unpicked}개 AI 검토에 고르기` : "강함·보통 후보를 모두 골랐습니다"}
        </button>
      )}
    </div>
  );
}
