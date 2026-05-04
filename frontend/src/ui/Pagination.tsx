import type { Component } from "solid-js";
import { Button } from "./Button";

type Props = {
  current: number;
  total: number;
  onPageChange: (page: number) => void;
  showing?: number;
  totalCount?: number;
};

export const Pagination: Component<Props> = (props) => {
  return (
    <div class="flex items-center gap-3 text-sm text-text-secondary">
      <span class="mr-auto">
        {props.showing !== undefined && props.totalCount !== undefined ? (
          <>
            Showing <span class="tabular text-text-primary">{props.showing}</span> of{" "}
            <span class="tabular text-text-primary">{props.totalCount}</span>
          </>
        ) : null}
      </span>
      <Button
        variant="secondary"
        size="sm"
        disabled={props.current <= 1}
        onClick={() => props.onPageChange(props.current - 1)}
      >
        Prev
      </Button>
      <span class="tabular text-xs">
        Page <span class="text-text-primary">{props.current}</span> of{" "}
        <span class="text-text-primary">{props.total}</span>
      </span>
      <Button
        variant="secondary"
        size="sm"
        disabled={props.current >= props.total}
        onClick={() => props.onPageChange(props.current + 1)}
      >
        Next
      </Button>
    </div>
  );
};
