import type { JSX, ParentComponent } from "solid-js";
import { createContext, useContext, Show } from "solid-js";
import { Icon } from "./icons";

export type SortOrder = "asc" | "desc";

type TableContextValue = {
  collapse: "none" | "card";
  sortField?: string;
  sortOrder?: SortOrder;
  onSort?: (field: string) => void;
};

const TableContext = createContext<TableContextValue>({ collapse: "none" });

type TableProps = {
  collapse?: "none" | "card";
  sortField?: string;
  sortOrder?: SortOrder;
  onSort?: (field: string) => void;
  class?: string;
} & JSX.HTMLAttributes<HTMLTableElement>;

const TableRoot: ParentComponent<TableProps> = (props) => {
  const ctx: TableContextValue = {
    get collapse() {
      return props.collapse ?? "none";
    },
    get sortField() {
      return props.sortField;
    },
    get sortOrder() {
      return props.sortOrder;
    },
    get onSort() {
      return props.onSort;
    },
  };
  return (
    <TableContext.Provider value={ctx}>
      <table
        class={`w-full text-sm ${props.collapse === "card" ? "ipa-table-collapse" : ""} ${props.class ?? ""}`}
      >
        {props.children}
      </table>
    </TableContext.Provider>
  );
};

const THead: ParentComponent<JSX.HTMLAttributes<HTMLTableSectionElement>> = (props) => (
  <thead {...props}>{props.children}</thead>
);

const TBody: ParentComponent<JSX.HTMLAttributes<HTMLTableSectionElement>> = (props) => (
  <tbody {...props}>{props.children}</tbody>
);

const TR: ParentComponent<JSX.HTMLAttributes<HTMLTableRowElement>> = (props) => (
  <tr {...props}>{props.children}</tr>
);

type THProps = {
  name?: string;
  sortable?: boolean;
  align?: "left" | "center" | "right";
  width?: number | string;
  class?: string;
} & JSX.ThHTMLAttributes<HTMLTableCellElement>;

const TH: ParentComponent<THProps> = (props) => {
  const ctx = useContext(TableContext);
  const alignCls = () =>
    props.align === "center"
      ? "text-center"
      : props.align === "right"
        ? "text-right"
        : "text-left";
  const sorted = () => props.sortable && ctx.sortField === props.name;
  const ariaSort = (): "ascending" | "descending" | "none" => {
    if (!sorted()) return "none";
    return ctx.sortOrder === "asc" ? "ascending" : "descending";
  };
  const handleClick = () => {
    if (!props.sortable || !props.name) return;
    ctx.onSort?.(props.name);
  };
  return (
    <th
      scope="col"
      style={
        props.width !== undefined ? { width: typeof props.width === "number" ? `${props.width}px` : props.width } : undefined
      }
      class={`border-b border-border px-3 py-2 text-[11px] font-semibold uppercase tracking-[0.05em] text-text-secondary ${alignCls()} ${props.sortable ? "cursor-pointer select-none hover:text-text-primary" : ""} ${props.class ?? ""}`}
      aria-sort={props.sortable ? ariaSort() : undefined}
      tabindex={props.sortable ? 0 : undefined}
      role={props.sortable ? "button" : undefined}
      onClick={handleClick}
      onKeyDown={(e) => {
        if (!props.sortable) return;
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          handleClick();
        }
      }}
    >
      <span class="inline-flex items-center gap-1">
        {props.children}
        <Show when={sorted()}>
          <Icon name={ctx.sortOrder === "asc" ? "chevron-up" : "chevron-down"} size={12} />
        </Show>
      </span>
    </th>
  );
};

type TDProps = {
  align?: "left" | "center" | "right";
  muted?: boolean;
  primary?: boolean;
  tabular?: boolean;
  label?: string;
  class?: string;
} & JSX.TdHTMLAttributes<HTMLTableCellElement>;

const TD: ParentComponent<TDProps> = (props) => {
  const alignCls = () =>
    props.align === "center"
      ? "text-center"
      : props.align === "right"
        ? "text-right"
        : "text-left";
  const colourCls = () =>
    props.muted ? "text-text-secondary" : props.primary ? "text-text-primary font-medium" : "";
  return (
    <td
      data-label={props.label}
      class={`border-b border-border-subtle px-3 py-2.5 align-middle ${alignCls()} ${colourCls()} ${props.tabular ? "tabular" : ""} ${props.class ?? ""}`}
    >
      {props.children}
    </td>
  );
};

export const Table = Object.assign(TableRoot, {
  THead,
  TBody,
  TR,
  TH,
  TD,
});

export { THead, TBody, TR, TH, TD };
