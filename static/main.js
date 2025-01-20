function nameFormatter(value, row) {
	let DisplayName = row.DisplayName;
	let ClearName = row.ClearName;
	let Account = row.Account !== undefined ? row.Account : row.AccountID;
	let Identity = row.Identity !== undefined ? row.Identity : row.Identity;
	let IdentityPubKey = row.IdentityPubKey;
	console.log(row);
	let DisplayNameClasses = '';
	if(DisplayName == '') {
		DisplayName = IdentityPubKey;
		DisplayNameClasses = 'text-secondary'
	}
	if(DisplayName !== undefined && DisplayName.length > 23) {
		DisplayName = DisplayName.slice(0, 20) + '...';
	}
	let Rating = row.Rating;
	let rr = {top: "", middle: "", bottom: "", customIcon: "",
		medal: `<object class="rank rank-pacifier"></object>`,
		text: `<small class="text-muted class="text-nowrap"">not registered</small>`,
	};
	if(Account !== undefined && Account > 0) {
		try {
			rr = nameFormatterRating(rr, Rating);
		} catch (e) {
			rr.text = e;
		}
	}
	let ret = `<div align="left" style="height:45px;">
	<table cellspacing="0" cellpadding="0" style="margin: 0">
	<tbody><tr>`;
	let dn = document.createElement('text');
	dn.appendChild(document.createTextNode(DisplayName));
	let playerURL = `/players/`+IdentityPubKey;
	if(Account !== undefined && Account > 0 && ClearName != '') {
		playerURL = `/players/`+ClearName;
	}
	let playerLink = `<a href="${playerURL}" class="text-nowrap ${DisplayNameClasses}">${dn.outerHTML}</a>`;
	if(rr.customIcon == "") {
		ret += `<td class="rank-star">`;
		ret += rr.top;
		ret += `</td><td rowspan="3" class="rank-medal">`;
		ret += rr.medal;
		ret += `</td><td rowspan="3" class="rank-link">`;
		ret += playerLink;
		ret += `<br>`;
		ret += rr.text;
		ret += `</td></tr><tr><td class="rank-star">`;
		ret += rr.middle;
		ret += `</td></tr><tr><td class="rank-star">`;
		ret += rr.bottom;
		ret += `</td>`;
	} else {
		ret += `<td>`;
		ret += rr.customIcon;
		ret += `</td><td class="rank-link">`;
		ret += playerLink;
		ret += `<br>`;
		ret += rr.text;
		ret += `</td></tr>`;
	}
	
	ret += `</tr></tbody></table></div>`;
	return ret;
}
function nameFormatterRating(dret, r) {
	let ret = dret;
	if(r == null) {
		ret.text = `<small class="text-secondary text-nowrap">not rated</small>`;
		ret.medal = ``;
		return ret
	}
	if(r.t == "elo") {
		if(r.played > 4) {
			ret.medal = ``;
			if(r.lost == 0) {
			} else if(r.won >= 24 && r.won/r.lost > 6) {
				ret.medal = `<object class="rank rank-medalGold"></object>`;
			} else if(r.won >= 12 && r.won/r.lost > 4) {
				ret.medal = `<object class="rank rank-medalDouble"></object>`;
			} else if(r.won >= 6 && r.won/r.lost > 3) {
				ret.medal = `<object class="rank rank-medalSilver"></object>`;
			}
		} else {
			ret.medal = `<object class="rank rank-pacifier"></object>`;
		}
		if(r.played > 4) {
			if(r.elo > 1800) {
				ret.top = `<object class="rank rank-starGold"></object>`;
			} else if(r.elo > 1550) {
				ret.top = `<object class="rank rank-starSilver"></object>`;
			} else if(r.elo > 1400) {
				ret.top = `<object class="rank rank-starBronze"></object>`;
			}
		}
		if(r.played > 60) {
			ret.middle = `<object class="rank rank-starGold"></object>`;
		} else if(r.played > 30) {
			ret.middle = `<object class="rank rank-starSilver"></object>`;
		} else if(r.played > 10) {
			ret.middle = `<object class="rank rank-starBronze"></object>`;
		}
		if(r.played > 4) {
			if(r.won > 60) {
				ret.bottom = `<object class="rank rank-starGold"></object>`;
			} else if(r.won > 30) {
				ret.bottom = `<object class="rank rank-starSilver"></object>`;
			} else if(r.won > 10) {
				ret.bottom = `<object class="rank rank-starBronze"></object>`;
			}
		}
		ret.text = `${r.elo}`;
	} else if(r.t == "botwl") {
		ret.customIcon = `<img src="/static/favicon.ico" width="27px">`;
		ret.text = `${r.played} played ${r.won} wins`;
	} else {
		ret.text = `?rating category`;
	}
	return ret;
}

function renderPlayers() {
	let pls = document.querySelectorAll("div[loadPlayer]");
	for (const pl of pls) {
		let ob = JSON.parse(pl.attributes['loadplayer'].nodeValue);
		pl.outerHTML = nameFormatter(null, ob);
	}
}

function renderTimestamps() {
	let ts = document.querySelectorAll("time[datetime]");
	for (const t of ts) {
		let d = new Date(t.attributes['datetime'].nodeValue);
		t.textContent = d.toLocaleString();
	}
}
