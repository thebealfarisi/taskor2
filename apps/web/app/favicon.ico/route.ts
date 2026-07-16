export function GET(request: Request) {
  return Response.redirect(new URL("/favicon.svg?v=2", request.url), 308);
}
