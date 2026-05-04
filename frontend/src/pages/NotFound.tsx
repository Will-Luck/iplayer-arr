import { Card } from "../ui/Card";
import { EmptyState } from "../ui/EmptyState";
import { Button } from "../ui/Button";

export default function NotFound() {
  return (
    <div class="flex flex-col gap-4">
      <h1 class="page-title">Page not found</h1>
      <Card>
        <Card.Body padded={false}>
          <EmptyState
            icon="alert"
            title="The URL you requested does not match any page in iplayer-arr."
            action={
              <Button variant="secondary" size="sm" onClick={() => (window.location.href = "/")}>
                Return to dashboard
              </Button>
            }
          />
        </Card.Body>
      </Card>
    </div>
  );
}
