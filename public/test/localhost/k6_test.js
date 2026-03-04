import http from 'k6/http';
import { sleep, check } from 'k6';

export const options = {
    vus: 10,
    duration: '5s',
};

export default function () {
    const res = http.get('http://localhost:8080/hello');
    check(res, {
        'status is 200': (r) => r.status === 200,
        'body is hello': (r) => r.body === 'hello world',
    });
}
