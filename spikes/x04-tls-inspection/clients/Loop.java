// X04 spike: Java HTTPS client. java Loop.java <url> [count] [interval s].
// Each request uses a new HttpClient. Throwaway.
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.time.Instant;

public class Loop {
    public static void main(String[] a) throws Exception {
        String url = a[0];
        int n = a.length > 2 ? Integer.parseInt(a[1]) : 1;
        int every = a.length > 2 ? Integer.parseInt(a[2]) : 0;
        boolean ok = false;
        for (int i = 0; i < n; i++) {
            if (i > 0) Thread.sleep(every * 1000L);
            String out;
            try (HttpClient c = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(20)).build()) {
                int s = c.send(HttpRequest.newBuilder(URI.create(url)).build(), HttpResponse.BodyHandlers.discarding()).statusCode();
                out = "{\"t\":\"" + Instant.now() + "\",\"i\":" + i + ",\"status\":" + s + "}";
                ok = true;
            } catch (Exception e) {
                Throwable r = e;
                while (r.getCause() != null) r = r.getCause();
                out = "{\"t\":\"" + Instant.now() + "\",\"i\":" + i + ",\"err\":\"" + (e + " / " + r).replace("\"", "'") + "\"}";
                ok = false;
            }
            System.out.println(out);
        }
        System.exit(ok ? 0 : 1);
    }
}
