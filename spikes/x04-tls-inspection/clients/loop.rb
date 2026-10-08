# X04 spike: Ruby HTTPS client. ruby loop.rb <url> [count] [interval s]. Throwaway.
require "net/http"
require "json"
require "time"
url, n, every = ARGV[0], (ARGV[1] || 1).to_i, (ARGV[2] || 0).to_i
ok = false
n.times do |i|
  sleep every if i > 0
  out = { t: Time.now.utc.iso8601, i: i }
  begin
    u = URI(url)
    Net::HTTP.start(u.host, u.port, use_ssl: true, open_timeout: 20, read_timeout: 20) do |h|
      out[:status] = h.request(Net::HTTP::Get.new(u)).code.to_i
    end
    ok = true
  rescue => e
    out[:err] = "#{e.class}: #{e.message}"
    ok = false
  end
  puts out.to_json
  $stdout.flush
end
exit(ok ? 0 : 1)
