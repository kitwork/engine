const w = work("pricegood")
    .router("GET", "/api/gold")
    .cache("5s")
    .handle(() => {
        // Gọi API PNJ thật
        let res = http.get("https://edge-api.pnj.io/ecom-frontend/v1/get-gold-price?zone=11");

        if (res.status != 200) {
            return { status: res.status, error: "Failed to fetch gold price" };
        }

        log("test cache")

        const data = res.body.data.map(item => ({
            name: item.tensp,
            buy: item.giamua,
            sell: item.giaban
        })).filter(item => item.sell > 10000);

        return {
            success: true, data: data, count: data.length
        };
    })
    .version("1.5.0");