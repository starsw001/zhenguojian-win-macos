package licensing

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
	"syscall"
	"time"
)

// recoverableConnection 判断授权连接失败是否属于可恢复的网络错误。
func recoverableConnection(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, errLicenseProxy) || errors.Is(err, errLicenseRedirect) {
		return false
	}
	var cert *tls.CertificateVerificationError
	var dns *net.DNSError
	if errors.As(err, &cert) || errors.As(err, &dns) && dns.IsNotFound {
		return false
	}
	var ne net.Error
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.Errno(10054)) ||
		errors.Is(err, syscall.Errno(10053)) || errors.As(err, &ne) && ne.Timeout()
}

// sendLicenseRequest 通过指定客户端发送授权请求并处理连接回退。
func sendLicenseRequest(client *http.Client, req *http.Request) (*http.Response, error) {
	base, ok := client.Transport.(*http.Transport)
	if !ok {
		return client.Do(req)
	}
	ctx, cancel := context.WithTimeout(req.Context(), 16*time.Second)
	var acquired, sent atomic.Bool
	trace := &httptrace.ClientTrace{
		GotConn:      func(httptrace.GotConnInfo) { acquired.Store(true) },
		WroteHeaders: func() { sent.Store(true) },
		WroteRequest: func(httptrace.WroteRequestInfo) { sent.Store(true) },
	}
	first := req.Clone(httptrace.WithClientTrace(ctx, trace))
	res, err := client.Do(first)
	if err == nil {
		res.Body = &licenseResponseBody{ReadCloser: res.Body, onClose: cancel}
		return res, nil
	}
	if res != nil || ctx.Err() != nil || !recoverableConnection(err) || req.Method != http.MethodGet && (acquired.Load() || sent.Load()) || req.Body != nil && req.GetBody == nil {
		cancel()
		return res, err
	}
	next := req.Clone(ctx)
	if req.Body != nil {
		next.Body, err = req.GetBody()
		if err != nil {
			cancel()
			return nil, err
		}
	}
	transport := base.Clone()
	transport.ForceAttemptHTTP2 = false
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	if transport.TLSClientConfig != nil {
		transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
	}
	fallback := *client
	fallback.Transport = transport
	res, err = fallback.Do(next)
	if err != nil {
		cancel()
		transport.CloseIdleConnections()
		return res, err
	}
	res.Body = &licenseResponseBody{ReadCloser: res.Body, onClose: func() { cancel(); transport.CloseIdleConnections() }}
	return res, nil
}

type licenseResponseBody struct {
	io.ReadCloser
	onClose func()
}

// Close 关闭许可证响应正文并释放其持有的资源。
func (b *licenseResponseBody) Close() error { err := b.ReadCloser.Close(); b.onClose(); return err }
